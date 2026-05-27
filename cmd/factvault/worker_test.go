package main

import (
	"strings"
	"testing"
)

func TestWorkerExtractRejectsInvalidProviderBeforeDB(t *testing.T) {
	cmd := newWorkerCmd()
	cmd.SetArgs([]string{
		"extract",
		"--tenant", "11111111-1111-1111-1111-111111111111",
		"--dsn", "postgres://127.0.0.1:1/nope",
		"--llm-provider", "quantum",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid llm provider") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWorkerExtractRejectsInvalidVocabularyModeBeforeDB(t *testing.T) {
	cmd := newWorkerCmd()
	cmd.SetArgs([]string{
		"extract",
		"--tenant", "11111111-1111-1111-1111-111111111111",
		"--dsn", "postgres://127.0.0.1:1/nope",
		"--vocabulary-mode", "strcit",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid vocabulary mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}
