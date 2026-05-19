package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/blendbyte/tindra/internal/storage"
)

func usersCmd(cfg config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "users",
		Short: "Manage users",
	}
	cmd.AddCommand(usersCreateCmd(cfg), usersListCmd(cfg))
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
