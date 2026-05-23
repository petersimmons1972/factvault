package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/agentcomms"
)

func newHandoffCmd() *cobra.Command {
	var from, issue, commit, summary string
	cmd := &cobra.Command{
		Use:   "handoff",
		Short: "Send a handoff message to Claude",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, _ := cmd.Flags().GetString("root")
			store, err := agentcomms.NewStore(root)
			if err != nil {
				return err
			}
			id, err := agentcomms.NewULID(time.Now())
			if err != nil {
				return err
			}
			body := summary
			if body == "" {
				body = fmt.Sprintf("handoff issue=%s commit=%s", issue, commit)
			}
			msg := &agentcomms.Message{
				ID:   id,
				From: agentcomms.Agent(from),
				To:   agentcomms.AgentClaude,
				TS:   time.Now().UTC().Format(time.RFC3339),
				Kind: agentcomms.KindHandoff,
				Refs: splitCSV(fmt.Sprintf("#%s,commit:%s", issue, commit)),
				Body: body,
			}
			if err := store.Send(msg); err != nil {
				return err
			}
			fmt.Println(id)
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "sender [required]")
	cmd.Flags().StringVar(&issue, "issue", "", "issue number [required]")
	cmd.Flags().StringVar(&commit, "commit", "", "commit SHA [required]")
	cmd.Flags().StringVar(&summary, "summary", "", "handoff summary")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("issue")
	_ = cmd.MarkFlagRequired("commit")
	return cmd
}
