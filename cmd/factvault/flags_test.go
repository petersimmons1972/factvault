package main

import (
	"strings"
	"testing"
)

func TestLLMCostGuardrailThresholdHelpIncludesUnit(t *testing.T) {
	cmd := newWorkerCmd()

	flag := cmd.PersistentFlags().Lookup("llm-cost-guardrail-threshold")
	if flag == nil {
		t.Fatal("expected llm-cost-guardrail-threshold flag to be registered")
	}

	if got := strings.ToLower(flag.Usage); !strings.Contains(got, "extractions") {
		t.Fatalf("flag help = %q, want unit to mention extractions", flag.Usage)
	}
}
