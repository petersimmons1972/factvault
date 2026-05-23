package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/agentcomms"
)

func newBlockCmd() *cobra.Command {
	var from, code, severity, body string
	cmd := &cobra.Command{
		Use:   "block",
		Short: "Send a blocking message to Claude",
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
			msgBody := body
			if msgBody == "" {
				msgBody = fmt.Sprintf("block code=%s severity=%s", code, severity)
			}
			msg := &agentcomms.Message{
				ID:   id,
				From: agentcomms.Agent(from),
				To:   agentcomms.AgentClaude,
				TS:   time.Now().UTC().Format(time.RFC3339),
				Kind: agentcomms.KindBlock,
				Refs: splitCSV(fmt.Sprintf("code:%s", code)),
				Body: msgBody,
			}
			if err := store.Send(msg); err != nil {
				return err
			}
			fmt.Println(id)
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "sender [required]")
	cmd.Flags().StringVar(&code, "code", "unspecified", "block code")
	cmd.Flags().StringVar(&severity, "severity", "error", "severity label for the message body")
	cmd.Flags().StringVar(&body, "body", "", "block body (default: derived from code/severity)")
	_ = cmd.MarkFlagRequired("from")
	return cmd
}
