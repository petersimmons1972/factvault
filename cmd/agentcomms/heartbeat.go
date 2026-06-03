package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/agentcomms"
)

func newHeartbeatCmd() *cobra.Command {
	var body, from string
	cmd := &cobra.Command{
		Use:   "heartbeat",
		Short: "Emit a heartbeat message (nudge kind, body=status)",
		Args:  cobra.NoArgs,
		Long: "Heartbeat is shipped as a `nudge` to the peer with a status body.\n" +
			"Schema v1 has no dedicated `heartbeat` kind; v2 uses `nudge` until\n" +
			"the schema is extended (see §20.7 queue_depth backpressure TODO).",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := cmd.Flags().GetString("root")
			if err != nil {
				return err
			}
			store, err := agentcomms.NewStore(root)
			if err != nil {
				return err
			}
			fromAgent := agentcomms.Agent(from)
			toAgent := agentcomms.AgentClaude
			if fromAgent == agentcomms.AgentClaude {
				toAgent = agentcomms.AgentCodex
			}
			id, err := agentcomms.NewULID(time.Now())
			if err != nil {
				return err
			}
			depth, err := store.QueueDepth(toAgent)
			if err != nil {
				return err
			}
			b := body
			if b == "" {
				b = fmt.Sprintf("heartbeat from %s; recipient queue_depth=%d", fromAgent, depth)
			}
			msg := &agentcomms.Message{
				ID:   id,
				From: fromAgent,
				To:   toAgent,
				TS:   time.Now().UTC().Format(time.RFC3339),
				Kind: agentcomms.KindNudge,
				Refs: []string{"heartbeat"},
				Body: b,
			}
			if err := store.Send(msg); err != nil {
				return err
			}
			fmt.Println(id)
			return nil
		},
	}
	cmd.Flags().StringVar(&body, "body", "", "heartbeat body (default: auto-generated)")
	cmd.Flags().StringVar(&from, "from", "", "sender [required]")
	if err := cmd.MarkFlagRequired("from"); err != nil {
		panic(err)
	}
	return cmd
}
