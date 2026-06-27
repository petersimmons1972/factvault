// Package main implements the factvault CLI entrypoint and command wiring.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/version"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := newRootCmd()
	if err := root.ExecuteContext(ctx); err != nil {
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
	root.AddCommand(newBriefCmd())
	root.AddCommand(newAuthCmd())
	root.AddCommand(newAPICmd())
	root.AddCommand(newMCPCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newExampleCmd())
	return root
}
