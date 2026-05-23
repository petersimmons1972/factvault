// agentcomms is the Go v2 of the factvault agent message bus CLI.
// See .agent-comms/README.md and .agent-comms/schema.json for the spec, and
// GitHub issue #85 for the v2 deliverable scope.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "agentcomms",
		Short: "Filesystem message bus between Claude and Codex (Go v2)",
		Long: "agentcomms is the factvault agent message bus CLI.\n" +
			"Messages live under .agent-comms/inbox/<recipient>/ as JSON files.\n" +
			"See .agent-comms/README.md and schema.json.",
	}
	root.PersistentFlags().String("root", ".agent-comms", "path to .agent-comms directory")

	root.AddCommand(
		newSendCmd(),
		newReadCmd(),
		newAckCmd(),
		newHeartbeatCmd(),
		newClaimCmd(),
		newQuestionCmd(),
		newBlockCmd(),
		newHandoffCmd(),
		newCapabilityCmd(),
		newLessonsCmd(),
		newArchiveCmd(),
		newHealthCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		// Default to exit 1 (protocol violation). Specific commands may exit 2
		// for transport errors via os.Exit explicitly.
		os.Exit(1)
	}
}
