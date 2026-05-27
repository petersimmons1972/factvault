package workers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerifySourceRejectsLoopbackAndInvalidScheme(t *testing.T) {
	p := &SourcePipeline{HTTPClient: &http.Client{}}
	if status, _, notes := p.verifySource(context.Background(), "http://127.0.0.1/private", "x"); status != "link-rot" || notes == "" {
		t.Fatalf("expected blocked loopback, got status=%q notes=%q", status, notes)
	}
	if status, _, notes := p.verifySource(context.Background(), "file:///etc/passwd", "x"); status != "link-rot" || notes == "" {
		t.Fatalf("expected blocked scheme, got status=%q notes=%q", status, notes)
	}
}

func TestVerifySourceRejectsLargeResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := make([]byte, maxVerifyBodyBytes+1)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	p := &SourcePipeline{HTTPClient: server.Client()}
	status, _, notes := p.verifySource(context.Background(), server.URL, "x")
	if status != "link-rot" {
		t.Fatalf("status=%q want link-rot", status)
	}
	if notes == "" {
		t.Fatal("expected notes for oversized response")
	}
}

func TestVerifyClientBlocksRedirectToLoopback(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "private")
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	p := &SourcePipeline{}
	status, _, notes := p.verifySource(context.Background(), redirector.URL, "x")
	if status != "link-rot" {
		t.Fatalf("status=%q want link-rot", status)
	}
	if notes == "" {
		t.Fatal("expected redirect block notes")
	}
}
