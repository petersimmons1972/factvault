package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/version"
)

func main() {
	root := newRootCmd()

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "factvault",
		Short:   "factvault - verifiable fact database",
		Version: version.Version,
	}
	root.AddCommand(newMigrateCmd())
	root.AddCommand(newWorkerCmd())
	root.AddCommand(newAuthCmd())
	root.AddCommand(newAPICmd())
	root.AddCommand(newMCPCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newExampleCmd())
	return root
}
