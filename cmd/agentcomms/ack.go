package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/agentcomms"
)

func newAckCmd() *cobra.Command {
	var body, from string
	cmd := &cobra.Command{
		Use:   "ack MSG_ID",
		Short: "Acknowledge a message and archive it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, _ := cmd.Flags().GetString("root")
			store, err := agentcomms.NewStore(root)
			if err != nil {
				return err
			}
			msgID := args[0]
			// Locate the original to determine target inbox + recipient.
			orig, err := findMessage(store, msgID)
			if err != nil {
				return err
			}
			ackTo := orig.From // ack returns to the sender
			ackFrom := orig.To
			if from != "" {
				ackFrom = agentcomms.Agent(from)
			}
			id, err := agentcomms.NewULID(time.Now())
			if err != nil {
				return err
			}
			ackMsg := &agentcomms.Message{
				ID:        id,
				From:      ackFrom,
				To:        ackTo,
				TS:        time.Now().UTC().Format(time.RFC3339),
				Kind:      agentcomms.KindAck,
				Refs:      []string{"msg:" + msgID},
				InReplyTo: &msgID,
				Body:      body,
			}
			if err := store.Send(ackMsg); err != nil {
				return fmt.Errorf("send ack: %w", err)
			}
			if err := store.Archive(msgID, "acked"); err != nil {
				return fmt.Errorf("archive original: %w", err)
			}
			fmt.Println(id)
			return nil
		},
	}
	cmd.Flags().StringVar(&body, "body", "ok", "ack body")
	cmd.Flags().StringVar(&from, "from", "", "ack sender (defaults to original recipient)")
	return cmd
}

// findMessage scans both inboxes for a message whose ID appears in the filename.
func findMessage(store *agentcomms.Store, msgID string) (*agentcomms.Message, error) {
	for _, a := range []agentcomms.Agent{agentcomms.AgentClaude, agentcomms.AgentCodex} {
		res, err := store.Read(agentcomms.ReadFilter{Inbox: a})
		if err != nil {
			return nil, err
		}
		for _, m := range res.Messages {
			if m.ID == msgID {
				return m, nil
			}
		}
	}
	return nil, fmt.Errorf("message %s not found in any inbox", msgID)
}
