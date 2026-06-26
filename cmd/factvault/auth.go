package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/petersimmons1972/factvault/internal/auth"
	"github.com/petersimmons1972/factvault/internal/config"
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
	var (
		jwtPrivateKeyPath string
		legacyPrivKeyPath string
		tenantID          string
		subject           string
		ttl               time.Duration
	)
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Issue a development RS256 JWT",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// C3: --jwt-private-key is primary; --private-key is a hidden deprecated alias.
			if legacyFlag := cmd.Flags().Lookup("private-key"); legacyFlag != nil && legacyFlag.Changed {
				fmt.Fprintln(os.Stderr, "warning: --private-key is deprecated; use --jwt-private-key")
				if jwtPrivateKeyPath == "" {
					jwtPrivateKeyPath = legacyPrivKeyPath
				}
			}
			// C9: flag > env (_FILE companion) > FACTVAULT_JWT_PRIVATE_KEY > required error.
			privateKeyPath := jwtPrivateKeyPath
			if privateKeyPath == "" {
				var err error
				privateKeyPath, err = config.ResolveSecret(nil, "FACTVAULT_JWT_PRIVATE_KEY", "", true)
				if err != nil {
					return err
				}
			}
			// C4: --tenant > FACTVAULT_DEV_TENANT_ID > ERROR.
			var err error
			tenantID, err = config.ResolveString(cmd.Flags().Lookup("tenant"), "FACTVAULT_DEV_TENANT_ID", "", true)
			if err != nil {
				return err
			}
			if subject == "" {
				return fmt.Errorf("--sub is required")
			}
			data, err := os.ReadFile(filepath.Clean(privateKeyPath))
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
	// C3: --jwt-private-key is the canonical flag; --private-key is hidden deprecated.
	cmd.Flags().StringVar(&jwtPrivateKeyPath, "jwt-private-key", "", "PEM private key path (or FACTVAULT_JWT_PRIVATE_KEY)")
	cmd.Flags().StringVar(&legacyPrivKeyPath, "private-key", "", "PEM private key path (deprecated: use --jwt-private-key)")
	if err := cmd.Flags().MarkHidden("private-key"); err != nil {
		panic(err)
	}
	cmd.Flags().StringVar(&tenantID, "tenant", "", "Tenant UUID (or FACTVAULT_DEV_TENANT_ID)")
	cmd.Flags().StringVar(&subject, "sub", "dev", "token subject")
	cmd.Flags().DurationVar(&ttl, "ttl", 24*time.Hour, "token TTL")
	return cmd
}

func newAuthVerifyCmd() *cobra.Command {
	var (
		jwtPublicKeyPath string
		legacyPubKeyPath string
		token            string
	)
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify an RS256 JWT",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// C3: --jwt-public-key is primary; --public-key is a hidden deprecated alias.
			if legacyFlag := cmd.Flags().Lookup("public-key"); legacyFlag != nil && legacyFlag.Changed {
				fmt.Fprintln(os.Stderr, "warning: --public-key is deprecated; use --jwt-public-key")
				if jwtPublicKeyPath == "" {
					jwtPublicKeyPath = legacyPubKeyPath
				}
			}
			// C9: flag > env (_FILE companion) > FACTVAULT_JWT_PUBLIC_KEY > required error.
			publicKeyPath := jwtPublicKeyPath
			if publicKeyPath == "" {
				var err error
				publicKeyPath, err = config.ResolveSecret(nil, "FACTVAULT_JWT_PUBLIC_KEY", "", true)
				if err != nil {
					return err
				}
			}
			if token == "" {
				return fmt.Errorf("--token is required")
			}
			data, err := os.ReadFile(filepath.Clean(publicKeyPath))
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
	// C3: --jwt-public-key is the canonical flag; --public-key is hidden deprecated.
	cmd.Flags().StringVar(&jwtPublicKeyPath, "jwt-public-key", "", "PEM public key path (or FACTVAULT_JWT_PUBLIC_KEY)")
	cmd.Flags().StringVar(&legacyPubKeyPath, "public-key", "", "PEM public key path (deprecated: use --jwt-public-key)")
	if err := cmd.Flags().MarkHidden("public-key"); err != nil {
		panic(err)
	}
	cmd.Flags().StringVar(&token, "token", "", "JWT to verify")
	return cmd
}
