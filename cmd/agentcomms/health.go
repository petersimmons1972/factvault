package main

import (
	"encoding/json"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/agentcomms"
)

func newHealthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Report bus health as JSON (schema_valid, queue depths, dead-letter count)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := cmd.Flags().GetString("root")
			if err != nil {
				return err
			}
			store, err := agentcomms.NewStore(root)
			if err != nil {
				return err
			}
			claudeDepth, err := store.QueueDepth(agentcomms.AgentClaude)
			if err != nil {
				return err
			}
			codexDepth, err := store.QueueDepth(agentcomms.AgentCodex)
			if err != nil {
				return err
			}

			// Run a stateless read on each inbox to surface dead-letter routing
			// counts and validate schema on every undrained file.
			claudeRes, err := store.Read(agentcomms.ReadFilter{Inbox: agentcomms.AgentClaude})
			if err != nil {
				return err
			}
			codexRes, err := store.Read(agentcomms.ReadFilter{Inbox: agentcomms.AgentCodex})
			if err != nil {
				return err
			}

			out := map[string]any{
				"schema_valid": (claudeRes != nil && codexRes != nil &&
					claudeRes.DeadLetter == 0 && codexRes.DeadLetter == 0),
				"queue_depth": map[string]int{
					"claude": claudeDepth,
					"codex":  codexDepth,
				},
				"soft_cap":    agentcomms.SoftQueueDepth,
				"hard_cap":    agentcomms.MaxQueueDepth,
				"checked_at":  time.Now().UTC().Format(time.RFC3339),
				"dead_letter": countDeadLetter(store),
				"root":        store.Root,
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(out)
		},
	}
	return cmd
}

func countDeadLetter(s *agentcomms.Store) int {
	entries, err := os.ReadDir(s.Root + "/dead-letter")
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// Count the canonical message files only, not `.error.json` sidecars.
		name := e.Name()
		if len(name) > len(".error.json") && name[len(name)-len(".error.json"):] == ".error.json" {
			continue
		}
		n++
	}
	return n
}
