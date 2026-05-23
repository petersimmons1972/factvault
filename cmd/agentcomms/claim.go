package main

import (
	"fmt"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/agentcomms"
)

func newClaimCmd() *cobra.Command {
	var from, repo string
	cmd := &cobra.Command{
		Use:   "claim ISSUE_NUM",
		Short: "Claim a GitHub issue and notify Claude",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, _ := cmd.Flags().GetString("root")
			store, err := agentcomms.NewStore(root)
			if err != nil {
				return err
			}
			issue := args[0]
			if repo != "" {
				if err := ghIssueEdit(repo, issue, []string{"agent/codex/working"}, []string{"agent/codex"}); err != nil {
					return err
				}
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
				Kind: agentcomms.KindClaim,
				Refs: []string{"#" + issue},
				Body: fmt.Sprintf("claimed issue #%s", issue),
			}
			if err := store.Send(msg); err != nil {
				return err
			}
			fmt.Println(id)
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", string(agentcomms.AgentCodex), "sender")
	cmd.Flags().StringVar(&repo, "repo", "", "GitHub repository owner/name; when set, update issue labels with gh")
	return cmd
}

func ghIssueEdit(repo, issue string, add, remove []string) error {
	args := []string{"issue", "edit", issue, "-R", repo}
	for _, label := range add {
		args = append(args, "--add-label", label)
	}
	for _, label := range remove {
		args = append(args, "--remove-label", label)
	}
	if out, err := exec.Command("gh", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("gh issue edit: %w: %s", err, string(out))
	}
	return nil
}
