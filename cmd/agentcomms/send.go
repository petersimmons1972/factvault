package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/agentcomms"
)

func newSendCmd() *cobra.Command {
	var from, to, kind, reply, body, refsCSV string
	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send a message to an agent inbox",
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
			id, err := agentcomms.NewULID(time.Now())
			if err != nil {
				return err
			}
			msg := &agentcomms.Message{
				ID:   id,
				From: agentcomms.Agent(from),
				To:   agentcomms.Agent(to),
				TS:   time.Now().UTC().Format(time.RFC3339),
				Kind: agentcomms.Kind(kind),
				Refs: splitCSV(refsCSV),
				Body: body,
			}
			if reply != "" {
				r := reply
				msg.InReplyTo = &r
			}
			if err := store.Send(msg); err != nil {
				return fmt.Errorf("send: %w", err)
			}
			fmt.Println(id)
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "sender agent (claude|codex) [required]")
	cmd.Flags().StringVar(&to, "to", "", "recipient agent (claude|codex) [required]")
	cmd.Flags().StringVar(&kind, "kind", "", "message kind [required]")
	cmd.Flags().StringVar(&reply, "reply", "", "ULID this replies to")
	cmd.Flags().StringVar(&refsCSV, "refs", "", "comma-separated refs (e.g. '#85,commit:abc')")
	cmd.Flags().StringVar(&body, "body", "", "message body [required]")
	if err := cmd.MarkFlagRequired("from"); err != nil {
		panic(err)
	}
	if err := cmd.MarkFlagRequired("to"); err != nil {
		panic(err)
	}
	if err := cmd.MarkFlagRequired("kind"); err != nil {
		panic(err)
	}
	if err := cmd.MarkFlagRequired("body"); err != nil {
		panic(err)
	}
	return cmd
}

func splitCSV(s string) []string {
	if s == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
