package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/db"
	fvexamples "github.com/petersimmons1972/factvault/internal/examples"
)

func newExampleCmd() *cobra.Command {
	var root string
	cmd := &cobra.Command{Use: "example", Short: "Inspect and load example domains"}
	cmd.PersistentFlags().StringVar(&root, "root", "examples", "examples root directory")
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List examples",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			names, err := fvexamples.List(root)
			if err != nil {
				return err
			}
			for _, name := range names {
				fmt.Fprintln(cmd.OutOrStdout(), name)
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "info NAME",
		Short: "Print example metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ex, err := fvexamples.Load(root, args[0])
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(ex)
		},
	})
	cmd.AddCommand(newExampleLoadCmd(&root))
	return cmd
}

func newExampleLoadCmd(root *string) *cobra.Command {
	var dsn, tenantID string
	cmd := &cobra.Command{
		Use:   "load NAME",
		Short: "Load example properties and seed entities",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dsn == "" {
				dsn = os.Getenv("FACTVAULT_DATABASE_URL")
			}
			if tenantID == "" {
				tenantID = os.Getenv("FACTVAULT_DEV_TENANT_ID")
			}
			if dsn == "" || tenantID == "" {
				return fmt.Errorf("--dsn and --tenant are required")
			}
			ex, err := fvexamples.Load(*root, args[0])
			if err != nil {
				return err
			}
			pool, err := db.NewPool(cmd.Context(), dsn)
			if err != nil {
				return err
			}
			defer pool.Close()
			if err := ex.Insert(cmd.Context(), pool, tenantID); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "loaded %s: %d properties, %d seeds\n", ex.Name, len(ex.Properties), len(ex.Seeds))
			return nil
		},
	}
	cmd.Flags().StringVar(&dsn, "dsn", "", "Postgres DSN")
	cmd.Flags().StringVar(&tenantID, "tenant", "", "tenant UUID")
	return cmd
}
