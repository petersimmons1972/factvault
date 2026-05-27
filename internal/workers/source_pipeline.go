package workers

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/petersimmons1972/factvault/internal/collectors"
)

type SourcePipeline struct {
	DB         *pgxpool.Pool
	HTTPClient *http.Client
}

const maxVerifyBodyBytes = 10 * 1024 * 1024

func (p *SourcePipeline) CollectOnce(ctx context.Context, tenantID string, c collectors.Collector) error {
	items, err := c.Collect(ctx)
	if err != nil {
		return fmt.Errorf("collect: %w", err)
	}
	for _, item := range items {
		if strings.TrimSpace(item.URL) == "" || len(item.HTML) == 0 {
			continue
		}
		if err := validateSourceURL(item.URL); err != nil {
			continue
		}
		hash := sha256Hex(item.HTML)
		compressed, err := compress(item.HTML)
		if err != nil {
			return err
		}
		_, err = p.DB.Exec(ctx, `
INSERT INTO sources (id, tenant_id, url, content_hash, raw_html, status)
VALUES ($1, $2, $3, $4, $5, 'collected')
ON CONFLICT (tenant_id, url) DO NOTHING
`, uuid.NewString(), tenantID, item.URL, hash, compressed)
		if err != nil {
			return fmt.Errorf("insert source: %w", err)
		}
	}
	return nil
}

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

func (p *SourcePipeline) VerifyOnce(ctx context.Context, tenantID string, ageDays int, limit int) error {
	if ageDays <= 0 {
		ageDays = 7
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := p.DB.Query(ctx, `
SELECT id, url, content_hash FROM sources
WHERE tenant_id = $1
  AND status IN ('archived', 'verified', 'content-changed', 'extracted')
  AND (last_verified_at IS NULL OR last_verified_at < $2)
ORDER BY fetched_at ASC
LIMIT $3
`, tenantID, time.Now().UTC().AddDate(0, 0, -ageDays), limit)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id, url, oldHash string
		if err := rows.Scan(&id, &url, &oldHash); err != nil {
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
`, mapSourceStatus(status), id)
		if err != nil {
			return err
		}
	}
	return rows.Err()
}

func (p *SourcePipeline) verifySource(ctx context.Context, url, oldHash string) (status, newHash, notes string) {
	if err := validateSourceURL(url); err != nil {
		return "link-rot", "", err.Error()
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	client := p.client()
	clone := *client
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return validateURLObject(req.URL)
	}
	resp, err := clone.Do(req)
	if err != nil {
		return "link-rot", "", err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "link-rot", "", fmt.Sprintf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxVerifyBodyBytes+1))
	if err != nil {
		return "link-rot", "", err.Error()
	}
	if len(body) > maxVerifyBodyBytes {
		return "link-rot", "", "response body too large"
	}
	newHash = sha256Hex(body)
	if newHash != oldHash {
		return "content-changed", newHash, ""
	}
	return "live", newHash, ""
}

func mapSourceStatus(verificationStatus string) string {
	switch verificationStatus {
	case "live":
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
	return &http.Client{Timeout: 20 * time.Second}
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
	defer zr.Close()
	return io.ReadAll(zr)
}

func stripHTML(html string) string {
	s := html
	repl := []string{"<br>", "\n", "<br/>", "\n", "<br />", "\n", "</p>", "\n", "</div>", "\n"}
	for i := 0; i < len(repl); i += 2 {
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
	defer resp.Body.Close()
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

func validateSourceURL(raw string) error {
	u, err := neturl.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid source url: %w", err)
	}
	return validateURLObject(u)
}

func validateURLObject(u *neturl.URL) error {
	if u == nil {
		return errors.New("invalid source url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if u.Hostname() == "" || u.User != nil {
		return errors.New("invalid host")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		if isPrivateOrInternalIP(ip) {
			return fmt.Errorf("blocked internal address %s", ip.String())
		}
		return nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(context.Background(), u.Hostname())
	if err != nil {
		return fmt.Errorf("resolve host: %w", err)
	}
	for _, addr := range addrs {
		if isPrivateOrInternalIP(addr.IP) {
			return fmt.Errorf("blocked internal address %s", addr.IP.String())
		}
	}
	return nil
}

func isPrivateOrInternalIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalMulticast() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsPrivate() {
		return true
	}
	// Block common cloud metadata endpoint.
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 169 && ip4[1] == 254 && ip4[2] == 169 && ip4[3] == 254 {
		return true
	}
	return false
}
