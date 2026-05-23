package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/agentcomms"
)

func newQuestionCmd() *cobra.Command {
	var from, body string
	cmd := &cobra.Command{
		Use:   "question",
		Short: "Send a question message to Claude",
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
			msg := &agentcomms.Message{
				ID:   id,
				From: agentcomms.Agent(from),
				To:   agentcomms.AgentClaude,
				TS:   time.Now().UTC().Format(time.RFC3339),
				Kind: agentcomms.KindQuestion,
				Refs: []string{},
				Body: body,
			}
			if msg.Body == "" {
				return fmt.Errorf("question body is required")
			}
			if err := store.Send(msg); err != nil {
				return err
			}
			fmt.Println(id)
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "sender [required]")
	cmd.Flags().StringVar(&body, "body", "", "question body [required]")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("body")
	return cmd
}
