package main

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestNoArgCommandsRejectUnexpectedPositionalArgs(t *testing.T) {
	tests := []struct {
		name string
		cmd  *cobra.Command
	}{
		{name: "init", cmd: newInitCmd()},
		{name: "doctor", cmd: newDoctorCmd()},
		{name: "migrate", cmd: newMigrateCmd()},
		{name: "api", cmd: newAPICmd()},
		{name: "mcp", cmd: newMCPCmd()},
		{name: "auth keys", cmd: newAuthKeysCmd()},
		{name: "auth token", cmd: newAuthTokenCmd()},
		{name: "auth verify", cmd: newAuthVerifyCmd()},
		{name: "worker collect", cmd: mustSubcommand(t, newWorkerCmd(), "collect")},
		{name: "worker archive", cmd: mustSubcommand(t, newWorkerCmd(), "archive")},
		{name: "worker extract", cmd: mustSubcommand(t, newWorkerCmd(), "extract")},
		{name: "worker verify", cmd: mustSubcommand(t, newWorkerCmd(), "verify")},
		{name: "worker corroborate", cmd: mustSubcommand(t, newWorkerCmd(), "corroborate")},
		{name: "worker dossier", cmd: mustSubcommand(t, newWorkerCmd(), "dossier")},
		{name: "worker rss", cmd: mustSubcommand(t, newWorkerCmd(), "rss")},
		{name: "brief generate", cmd: mustSubcommand(t, newBriefCmd(), "generate")},
		{name: "brief list", cmd: mustSubcommand(t, newBriefCmd(), "list")},
		{name: "example list", cmd: mustSubcommand(t, newExampleCmd(), "list")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.cmd.Args == nil {
				t.Fatal("expected Args validator to be set")
			}
			if err := tc.cmd.Args(tc.cmd, []string{"unexpected"}); err == nil {
				t.Fatal("expected no-arg validation error")
			}
		})
	}
}

func mustSubcommand(t *testing.T, parent *cobra.Command, name string) *cobra.Command {
	t.Helper()
	child, _, err := parent.Find([]string{name})
	if err != nil {
		t.Fatalf("find %q: %v", name, err)
	}
	return child
}
