package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/agentcomms"
)

func newReadCmd() *cobra.Command {
	var (
		inbox, kind, from string
		unread            bool
	)
	cmd := &cobra.Command{
		Use:   "read",
		Short: "Read messages from an inbox (JSON-encoded list)",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, _ := cmd.Flags().GetString("root")
			store, err := agentcomms.NewStore(root)
			if err != nil {
				return err
			}
			if inbox == "" {
				// Default inbox: read claude's. The Python ref defaults to the
				// caller agent; v2 keeps it explicit and asks --inbox or defaults
				// to claude so behaviour is deterministic.
				inbox = string(agentcomms.AgentClaude)
			}
			res, err := store.Read(agentcomms.ReadFilter{
				Inbox:  agentcomms.Agent(inbox),
				Kind:   agentcomms.Kind(kind),
				From:   agentcomms.Agent(from),
				Unread: unread,
			})
			if err != nil {
				return err
			}
			msgs := res.Messages
			if msgs == nil {
				msgs = []*agentcomms.Message{}
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(msgs)
		},
	}
	cmd.Flags().StringVar(&inbox, "inbox", "", "which inbox to read (claude|codex) [default: claude]")
	cmd.Flags().StringVar(&kind, "kind", "", "filter by kind")
	cmd.Flags().StringVar(&from, "from", "", "filter by sender")
	cmd.Flags().BoolVar(&unread, "unread", false, "only show unread (cursor not yet implemented; flag accepted)")
	// suppress unused-arg complaints; intentional.
	_ = fmt.Sprintln
	return cmd
}
