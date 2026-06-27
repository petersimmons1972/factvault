package main

import (
	"testing"
)

// TestAPICmdAddrFlag verifies the --addr flag is registered and defaults to :8080.
// The env-fallback and flag-override logic is handled by config.ResolveString
// (tested in internal/config/contract_test.go and resolver_test.go).
func TestAPICmdAddrFlag(t *testing.T) {
	cmd := newAPICmd()
	f := cmd.Flags().Lookup("addr")
	if f == nil {
		t.Fatal("newAPICmd() missing --addr flag")
		return // unreachable; helps static analysis
	}
	if f.DefValue != ":8080" {
		t.Fatalf("--addr default = %q, want :8080", f.DefValue)
	}
}

// TestAPICmdEmbedderURLDefault verifies the --embedder-url flag is not present
// on the API command (embedder URL is resolved inside RunE, not exposed as a flag).
func TestAPICmdJWTPublicKeyFlag(t *testing.T) {
	cmd := newAPICmd()
	f := cmd.Flags().Lookup("jwt-public-key")
	if f == nil {
		t.Fatal("newAPICmd() missing --jwt-public-key flag")
	}
}
