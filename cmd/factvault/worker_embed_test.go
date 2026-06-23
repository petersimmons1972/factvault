package main

import (
	"net/url"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/doctor"
)

func TestEmbedWorkerDefaultURLDoesNotCollideWithAPI(t *testing.T) {
	apiDefault := mustFlagDefault(t, newAPICmd(), "addr")
	embedderDefault := doctor.DefaultEmbedderURL

	apiPort := strings.TrimPrefix(apiDefault, ":")
	embedderURL, err := url.Parse(embedderDefault)
	if err != nil {
		t.Fatalf("parse embedder default %q: %v", embedderDefault, err)
	}
	embedderPort := embedderURL.Port()
	if embedderPort == "" {
		t.Fatalf("embedder default %q has no explicit port", embedderDefault)
	}
	if apiPort == embedderPort {
		t.Fatalf("API default port %q collides with embedder default %q", apiDefault, embedderDefault)
	}
}

func mustFlagDefault(t *testing.T, cmd *cobra.Command, name string) string {
	t.Helper()
	flag := cmd.Flags().Lookup(name)
	if flag == nil {
		t.Fatalf("flag %q not found", name)
	}
	return flag.DefValue
}
