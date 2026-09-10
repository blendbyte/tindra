package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/blendbyte/tindra/internal/storage"
)

func usersLinkSSOCmd(cfg config) *cobra.Command {
	var provider, subject string
	cmd := &cobra.Command{
		Use:   "link-sso <email>",
		Short: "Link an existing user to an SSO identity (server administrators only)",
		Long: "Link an existing Tindra account to a provider subject using administrative database access.\n\n" +
			"Use the exact provider name configured in Tindra, such as microsoft, and the sub\n" +
			"claim from a verified ID token issued for this Tindra application's client ID.\n" +
			"For Microsoft, sub is application-specific and is not the Azure Object ID (oid).\n" +
			"Confirm the identity with the provider administrator before linking it.\n\n" +
			"This supports providers without verified-email claims. Existing links cannot be\n" +
			"reassigned. The user's permissions and MFA requirements remain in effect.",
		Example: "  tindra users link-sso user@example.com --provider microsoft --subject '<verified-token-sub>'",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			email := strings.ToLower(strings.TrimSpace(args[0]))
			if email == "" || strings.TrimSpace(provider) == "" || strings.TrimSpace(subject) == "" {
				return fmt.Errorf("email, provider, and subject must not be blank")
			}
			ctx := cmd.Context()
			pool, err := storage.Connect(ctx, cfg.databaseURL)
			if err != nil {
				return err
			}
			defer pool.Close()
			user, err := storage.GetUserByEmail(ctx, pool, email)
			if err != nil {
				return fmt.Errorf("look up user: %w", err)
			}
			if user == nil {
				return fmt.Errorf("no user found with email %q", email)
			}
			if err := storage.LinkOAuthIdentity(ctx, pool, user.ID, provider, subject); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Linked %s to %s subject %q. Existing permissions and MFA requirements still apply.\n", user.Email, provider, subject)
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", "Exact configured provider name")
	cmd.Flags().StringVar(&subject, "subject", "", "Exact subject from the provider's verified ID token for this application")
	_ = cmd.MarkFlagRequired("provider")
	_ = cmd.MarkFlagRequired("subject")
	return cmd
}
