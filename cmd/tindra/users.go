package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/blendbyte/tindra/internal/alerts"
	"github.com/blendbyte/tindra/internal/storage"
)

func usersCmd(cfg config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "users",
		Short: "Manage users",
	}
	cmd.AddCommand(usersCreateCmd(cfg), usersListCmd(cfg), usersSendPasswordResetCmd(cfg), usersSendInviteCmd(cfg))
	return cmd
}

func usersCreateCmd(cfg config) *cobra.Command {
	var email, name, password string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new user",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			pool, err := storage.Connect(ctx, cfg.databaseURL)
			if err != nil {
				return err
			}
			defer pool.Close()

			if cfg.userLimit > 0 {
				count, err := storage.CountUsers(ctx, pool)
				if err != nil {
					return fmt.Errorf("count users: %w", err)
				}
				if count >= int64(cfg.userLimit) {
					return fmt.Errorf("user limit of %d reached (set USER_LIMIT to increase)", cfg.userLimit)
				}
			}

			user, err := storage.CreateAdminUser(ctx, pool, strings.ToLower(email), name, password)
			if err != nil {
				return fmt.Errorf("create user: %w", err)
			}
			fmt.Printf("Created user %s (id: %s) with full permissions\n", user.Email, user.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "User email address")
	cmd.Flags().StringVar(&name, "name", "", "Display name")
	cmd.Flags().StringVar(&password, "password", "", "User password (min 12 characters)")
	_ = cmd.MarkFlagRequired("email")
	_ = cmd.MarkFlagRequired("password")
	return cmd
}

func usersSendPasswordResetCmd(cfg config) *cobra.Command {
	return &cobra.Command{
		Use:   "send-password-reset <email>",
		Short: "Send a password reset email to a user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			pool, err := storage.Connect(ctx, cfg.databaseURL)
			if err != nil {
				return err
			}
			defer pool.Close()

			email := strings.ToLower(strings.TrimSpace(args[0]))
			u, err := storage.GetUserByEmail(ctx, pool, email)
			if err != nil {
				return fmt.Errorf("look up user: %w", err)
			}
			if u == nil {
				return fmt.Errorf("no user found with email %q", email)
			}

			token, err := storage.CreatePasswordResetToken(ctx, pool, u.ID)
			if err != nil {
				return fmt.Errorf("create reset token: %w", err)
			}

			resetURL := strings.TrimRight(cfg.publicURL, "/") + "/reset-password/" + token

			emailSender, err := alerts.NewEmailSenderFromEnv()
			if err != nil {
				return fmt.Errorf("email sender: %w", err)
			}
			if emailSender == nil {
				fmt.Printf("Email not configured. Share this reset link manually:\n%s\n", resetURL)
				return nil
			}

			html, text, err := alerts.RenderPasswordResetEmail(resetURL, cfg.publicURL)
			if err != nil {
				return fmt.Errorf("render email: %w", err)
			}
			if err := emailSender.Send(ctx, alerts.EmailMessage{
				To:      u.Email,
				Subject: "Reset your Tindra password",
				HTML:    html,
				Text:    text,
			}); err != nil {
				return fmt.Errorf("send email: %w", err)
			}

			fmt.Printf("Password reset email sent to %s\n", u.Email)
			return nil
		},
	}
}

func usersSendInviteCmd(cfg config) *cobra.Command {
	var email, name string
	cmd := &cobra.Command{
		Use:   "send-invite",
		Short: "Create an invite link (and send an email if configured)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			pool, err := storage.Connect(ctx, cfg.databaseURL)
			if err != nil {
				return err
			}
			defer pool.Close()

			if cfg.userLimit > 0 {
				count, err := storage.CountUsers(ctx, pool)
				if err != nil {
					return fmt.Errorf("count users: %w", err)
				}
				if count >= int64(cfg.userLimit) {
					return fmt.Errorf("user limit of %d reached (set USER_LIMIT to increase)", cfg.userLimit)
				}
			}

			token, err := storage.CreateInvite(ctx, pool, "", strings.ToLower(email), name)
			if err != nil {
				return fmt.Errorf("create invite: %w", err)
			}

			inviteURL := strings.TrimRight(cfg.publicURL, "/") + "/invite/" + token

			emailSender, err := alerts.NewEmailSenderFromEnv()
			if err != nil {
				return fmt.Errorf("email sender: %w", err)
			}
			if emailSender == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Email not configured. Share this invite link manually:\n%s\n", inviteURL)
				return nil
			}

			html, text, err := alerts.RenderInviteEmail(inviteURL, cfg.publicURL)
			if err != nil {
				return fmt.Errorf("render email: %w", err)
			}
			if err := emailSender.Send(ctx, alerts.EmailMessage{
				To:      email,
				Subject: "You've been invited to Tindra",
				HTML:    html,
				Text:    text,
			}); err != nil {
				return fmt.Errorf("send email: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Invite email sent to %s\n", email)
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "Email address to invite")
	cmd.Flags().StringVar(&name, "name", "", "Display name for the invited user (optional)")
	_ = cmd.MarkFlagRequired("email")
	return cmd
}

func usersListCmd(cfg config) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all users",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			pool, err := storage.Connect(ctx, cfg.databaseURL)
			if err != nil {
				return err
			}
			defer pool.Close()

			users, err := storage.ListUsers(ctx, pool)
			if err != nil {
				return fmt.Errorf("list users: %w", err)
			}

			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tEMAIL\tCREATED AT")
			for _, u := range users {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", u.ID, u.Email, u.CreatedAt.Format("2006-01-02 15:04:05"))
			}
			tw.Flush()
			return nil
		},
	}
}
