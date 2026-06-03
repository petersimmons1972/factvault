package main

import (
	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/agentcomms"
)

func newArchiveCmd() *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:   "archive MSG_ID",
		Short: "Move a message from inbox/ to processed/",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := cmd.Flags().GetString("root")
			if err != nil {
				return err
			}
			store, err := agentcomms.NewStore(root)
			if err != nil {
				return err
			}
			return store.Archive(args[0], reason)
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "manual", "archive reason (logged to audit)")
	return cmd
}
