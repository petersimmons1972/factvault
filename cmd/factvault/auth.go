package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/auth"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Manage development JWT keys and tokens"}
	cmd.AddCommand(newAuthKeysCmd(), newAuthTokenCmd(), newAuthVerifyCmd())
	return cmd
}

func newAuthKeysCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "keys",
		Short: "Generate a development RSA key pair",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			priv, pub, err := auth.GenerateKeyPair()
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\n%s", priv, pub); err != nil {
				return err
			}
			return nil
		},
	}
}

func newAuthTokenCmd() *cobra.Command {
	var privateKeyPath, tenantID, subject string
	var ttl time.Duration
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Issue a development RS256 JWT",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if tenantID == "" || subject == "" || privateKeyPath == "" {
				return fmt.Errorf("--tenant, --sub, and --private-key are required")
			}
			data, err := os.ReadFile(privateKeyPath)
			if err != nil {
				return err
			}
			key, err := auth.ParsePrivateKeyPEM(data)
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			token, err := auth.SignRS256(key, auth.Claims{TenantID: tenantID, Subject: subject, IssuedAt: now.Unix(), ExpiresAt: now.Add(ttl).Unix()})
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), token); err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&privateKeyPath, "private-key", "", "PEM private key path")
	cmd.Flags().StringVar(&tenantID, "tenant", "", "tenant UUID")
	cmd.Flags().StringVar(&subject, "sub", "dev", "token subject")
	cmd.Flags().DurationVar(&ttl, "ttl", 24*time.Hour, "token TTL")
	return cmd
}

func newAuthVerifyCmd() *cobra.Command {
	var publicKeyPath, token string
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify an RS256 JWT",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if publicKeyPath == "" || token == "" {
				return fmt.Errorf("--public-key and --token are required")
			}
			data, err := os.ReadFile(publicKeyPath)
			if err != nil {
				return err
			}
			key, err := auth.ParsePublicKeyPEM(data)
			if err != nil {
				return err
			}
			claims, err := (auth.Verifier{PublicKey: key}).Verify(token)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "tenant_id=%s sub=%s exp=%d\n", claims.TenantID, claims.Subject, claims.ExpiresAt); err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&publicKeyPath, "public-key", "", "PEM public key path")
	cmd.Flags().StringVar(&token, "token", "", "JWT to verify")
	return cmd
}
