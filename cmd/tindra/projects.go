package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/blendbyte/tindra/internal/storage"
)

func projectsCmd(cfg config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "projects",
		Short: "Manage projects",
	}
	cmd.AddCommand(
		projectsCreateCmd(cfg),
		projectsListCmd(cfg),
		projectsDeleteCmd(cfg),
	)
	return cmd
}

func projectsCreateCmd(cfg config) *cobra.Command {
	var name, slug string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new project",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" || slug == "" {
				return fmt.Errorf("--name and --slug are required")
			}
			pool, err := storage.Connect(cmd.Context(), cfg.databaseURL)
			if err != nil {
				return fmt.Errorf("connect: %w", err)
			}
			defer pool.Close()

			if cfg.projectLimit > 0 {
				count, err := storage.CountProjects(cmd.Context(), pool)
				if err != nil {
					return fmt.Errorf("count projects: %w", err)
				}
				if count >= int64(cfg.projectLimit) {
					return fmt.Errorf("project limit of %d reached", cfg.projectLimit)
				}
			}

			p, err := storage.CreateProject(cmd.Context(), pool, slug, name)
			if err != nil {
				return fmt.Errorf("create project: %w", err)
			}
			fmt.Printf("Created project %q\n  slug:       %s\n  public_key: %s\n", p.Name, p.Slug, p.PublicKey)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Display name")
	cmd.Flags().StringVar(&slug, "slug", "", "URL-safe identifier")
	return cmd
}

func projectsListCmd(cfg config) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all projects",
		RunE: func(cmd *cobra.Command, args []string) error {
			pool, err := storage.Connect(cmd.Context(), cfg.databaseURL)
			if err != nil {
				return fmt.Errorf("connect: %w", err)
			}
			defer pool.Close()

			projects, err := storage.ListProjects(cmd.Context(), pool)
			if err != nil {
				return fmt.Errorf("list projects: %w", err)
			}
			if len(projects) == 0 {
				fmt.Println("No projects.")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "SLUG\tNAME\tEVENTS\tPUBLIC KEY\tCREATED")
			for _, p := range projects {
				events := fmt.Sprintf("%d", p.EventCount)
				if cfg.eventLimit > 0 {
					events = fmt.Sprintf("%d / %d", p.EventCount, cfg.eventLimit)
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", p.Slug, p.Name, events, p.PublicKey, p.CreatedAt.Format("2006-01-02"))
			}
			return w.Flush()
		},
	}
}

func projectsDeleteCmd(cfg config) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <slug>",
		Short: "Delete a project by slug",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pool, err := storage.Connect(cmd.Context(), cfg.databaseURL)
			if err != nil {
				return fmt.Errorf("connect: %w", err)
			}
			defer pool.Close()

			found, err := storage.DeleteProject(cmd.Context(), pool, args[0])
			if err != nil {
				return fmt.Errorf("delete project: %w", err)
			}
			if !found {
				return fmt.Errorf("project %q not found", args[0])
			}
			fmt.Printf("Deleted project %q\n", args[0])
			return nil
		},
	}
}
