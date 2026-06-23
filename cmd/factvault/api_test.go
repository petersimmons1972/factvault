package main

import "testing"

func TestAPIAddrEnvFallback(t *testing.T) {
	t.Setenv("FACTVAULT_API_ADDR", "127.0.0.1:9090")

	got := resolveAPIAddr(":8080", false)
	if got != "127.0.0.1:9090" {
		t.Fatalf("resolveAPIAddr() = %q, want env fallback", got)
	}
}

func TestAPIAddrFlagOverridesEnv(t *testing.T) {
	t.Setenv("FACTVAULT_API_ADDR", "127.0.0.1:9090")

	got := resolveAPIAddr("127.0.0.1:8081", true)
	if got != "127.0.0.1:8081" {
		t.Fatalf("resolveAPIAddr() = %q, want explicit flag value", got)
	}
}
