package main

import (
	"fmt"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/agentcomms"
)

type capabilityRegistry struct {
	AgentID     string         `json:"agent_id"`
	ProfileHash string         `json:"profile_hash,omitempty"`
	Profile     map[string]any `json:"profile"`
}

func newCapabilityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "capability",
		Short: "Manage local agent capability metadata",
	}
	cmd.AddCommand(newCapabilityPublishCmd())
	return cmd
}

func newCapabilityPublishCmd() *cobra.Command {
	var from, profileHash string
	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Publish this agent's capability profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, _ := cmd.Flags().GetString("root")
			store, err := agentcomms.NewStore(root)
			if err != nil {
				return err
			}
			profile := map[string]any{
				"agent_id":            from,
				"runtime":             "go",
				"go_version":          runtime.Version(),
				"os":                  runtime.GOOS,
				"arch":                runtime.GOARCH,
				"last_published_ts":   time.Now().UTC().Format(time.RFC3339),
				"supported_commands":  []string{"send", "read", "ack", "heartbeat", "claim", "handoff", "block", "question", "capability publish", "lessons"},
				"supported_transport": "filesystem",
			}
			reg := capabilityRegistry{
				AgentID:     from,
				ProfileHash: profileHash,
				Profile:     profile,
			}
			if err := writeJSONFile(root+"/registry/"+from+".json", reg); err != nil {
				return err
			}
			id, err := agentcomms.NewULID(time.Now())
			if err != nil {
				return err
			}
			msg := &agentcomms.Message{
				ID:   id,
				From: agentcomms.Agent(from),
				To:   agentcomms.AgentClaude,
				TS:   time.Now().UTC().Format(time.RFC3339),
				Kind: agentcomms.KindCapPub,
				Refs: []string{"registry:" + from},
				Body: fmt.Sprintf("capability registry updated for %s", from),
			}
			if err := store.Send(msg); err != nil {
				return err
			}
			fmt.Println(id)
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", string(agentcomms.AgentCodex), "sender")
	cmd.Flags().StringVar(&profileHash, "profile-hash", "", "optional profile hash")
	return cmd
}
