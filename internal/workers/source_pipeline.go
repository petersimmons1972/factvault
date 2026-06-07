package workers

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/petersimmons1972/factvault/internal/collectors"
	"github.com/petersimmons1972/factvault/internal/netx"
)

// SourcePipeline coordinates collection, archiving, and re-verification of sources.
type SourcePipeline struct {
	DB         *pgxpool.Pool
	HTTPClient *http.Client
}

const maxVerifyBodyBytes = 10 * 1024 * 1024

// CollectOnce fetches source candidates and persists them as collected sources.
func (p *SourcePipeline) CollectOnce(ctx context.Context, tenantID string, c collectors.Collector) error {
	items, err := c.Collect(ctx)
	if err != nil {
		return fmt.Errorf("collect: %w", err)
	}
	for _, item := range items {
		if strings.TrimSpace(item.URL) == "" || len(item.HTML) == 0 {
			continue
		}
		hash := sha256Hex(item.HTML)
		compressed, err := compress(item.HTML)
		if err != nil {
			return err
		}
		publisher := strings.TrimSpace(item.Publisher)
		if item.Topic != "" || len(item.Tags) > 0 {
			publisher = strings.TrimSpace(publisher + " topic=" + item.Topic + " tags=" + strings.Join(item.Tags, ","))
		}
		_, err = p.DB.Exec(ctx, `
INSERT INTO sources (id, tenant_id, url, content_hash, raw_html, status, title, publisher, published_at)
VALUES ($1, $2, $3, $4, $5, 'collected', NULLIF($6, ''), NULLIF($7, ''), $8)
ON CONFLICT (tenant_id, url) DO NOTHING
`, uuid.NewString(), tenantID, item.URL, hash, compressed, item.Title, publisher, item.PublishedAt)
		if err != nil {
			return fmt.Errorf("insert source: %w", err)
		}
	}
	return nil
}

// ArchiveOnce fetches collected sources and stores archived text artifacts.
func (p *SourcePipeline) ArchiveOnce(ctx context.Context, tenantID string, limit int) error {
	if limit <= 0 {
		limit = 100
	}
	rows, err := p.DB.Query(ctx, `
SELECT id, url, raw_html FROM sources
WHERE tenant_id = $1 AND status = 'collected'
ORDER BY fetched_at ASC
LIMIT $2
`, tenantID, limit)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id, url string
		var rawHTML []byte
		if err := rows.Scan(&id, &url, &rawHTML); err != nil {
			return err
		}
		html, err := decompress(rawHTML)
		if err != nil {
			return err
		}
		text := stripHTML(string(html))
		archiveURL := submitWayback(ctx, p.client(), url)
		_, err = p.DB.Exec(ctx, `
UPDATE sources
SET raw_text = $1, archive_url = $2, status = 'archived'
WHERE id = $3
`, text, archiveURL, id)
		if err != nil {
			return err
		}
	}
	return rows.Err()
}

// VerifyOnce validates source links and records verification outcomes.
func (p *SourcePipeline) VerifyOnce(ctx context.Context, tenantID string, ageDays int, limit int) error {
	if ageDays <= 0 {
		ageDays = 7
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := p.DB.Query(ctx, `
SELECT id, url, content_hash, status FROM sources
WHERE tenant_id = $1
  AND status IN ('archived', 'extracted', 'verified', 'content-changed')
  AND (last_verified_at IS NULL OR last_verified_at < $2)
ORDER BY fetched_at ASC
LIMIT $3
`, tenantID, time.Now().UTC().AddDate(0, 0, -ageDays), limit)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id, url, oldHash, oldStatus string
		if err := rows.Scan(&id, &url, &oldHash, &oldStatus); err != nil {
			return err
		}
		status, newHash, notes := p.verifySource(ctx, url, oldHash)
		_, err := p.DB.Exec(ctx, `
INSERT INTO source_verifications (id, source_id, tenant_id, status, new_content_hash, notes)
VALUES ($1, $2, $3, $4, $5, $6)
`, uuid.NewString(), id, tenantID, status, nullable(newHash), nullable(notes))
		if err != nil {
			return err
		}
		_, err = p.DB.Exec(ctx, `
UPDATE sources
SET status = $1, last_verified_at = now()
WHERE id = $2
`, mapSourceStatus(status, oldStatus), id)
		if err != nil {
			return err
		}
	}
	return rows.Err()
}

func (p *SourcePipeline) verifySource(ctx context.Context, url, oldHash string) (status, newHash, notes string) {
	if err := netx.ValidatePublicHTTPURL(ctx, url); err != nil {
		return "link-rot", "", err.Error()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "link-rot", "", err.Error()
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return "link-rot", "", err.Error()
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "close: %v\n", err)
		}
	}()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "link-rot", "", fmt.Sprintf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxVerifyBodyBytes+1))
	if err != nil {
		return "link-rot", "", err.Error()
	}
	if len(body) > maxVerifyBodyBytes {
		return "link-rot", "", fmt.Sprintf("response too large: max %d bytes", maxVerifyBodyBytes)
	}
	newHash = sha256Hex(body)
	if newHash != oldHash {
		return "content-changed", newHash, ""
	}
	return "live", newHash, ""
}

func mapSourceStatus(verificationStatus, currentStatus string) string {
	switch verificationStatus {
	case "live":
		// Keep extracted sources extracted after successful re-verification.
		if currentStatus == "extracted" {
			return "extracted"
		}
		return "verified"
	case "content-changed":
		return "content-changed"
	default:
		return "link-rot"
	}
}

func (p *SourcePipeline) client() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return netx.NewSafeHTTPClient(20 * time.Second)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func compress(b []byte) ([]byte, error) {
	var out bytes.Buffer
	zw := zlib.NewWriter(&out)
	if _, err := zw.Write(b); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func decompress(b []byte) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := zr.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "close zlib reader: %v\n", err)
		}
	}()
	return io.ReadAll(zr)
}

func stripHTML(html string) string {
	s := html
	repl := []string{"<br>", "\n", "<br/>", "\n", "<br />", "\n", "</p>", "\n", "</div>", "\n"}
	for i := 0; i+1 < len(repl); i += 2 {
		s = strings.ReplaceAll(s, repl[i], repl[i+1])
	}
	inTag := false
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				b.WriteRune(r)
			}
		}
	}
	return strings.TrimSpace(strings.Join(strings.Fields(b.String()), " "))
}

func submitWayback(ctx context.Context, client *http.Client, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://web.archive.org/save/"+url, nil)
	if err != nil {
		return ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "close: %v\n", err)
		}
	}()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return ""
	}
	loc := resp.Header.Get("Content-Location")
	if loc == "" {
		return ""
	}
	if strings.HasPrefix(loc, "http") {
		return loc
	}
	return "https://web.archive.org" + loc
}

func nullable(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
