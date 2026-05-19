package main

import (
	"fmt"
	"strconv"

	migrate "github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/spf13/cobra"

	"github.com/blendbyte/tindra/migrations"
)

func newMigrator(cfg config) (*migrate.Migrate, error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("open migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, cfg.databaseURL)
	if err != nil {
		return nil, fmt.Errorf("init migrate: %w", err)
	}
	return m, nil
}

func migrateCmd(cfg config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Run pending database migrations",
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := newMigrator(cfg)
			if err != nil {
				return err
			}
			defer m.Close()
			if err := m.Up(); err != nil && err != migrate.ErrNoChange {
				return fmt.Errorf("migrate: %w", err)
			}
			fmt.Println("Migrations applied.")
			return nil
		},
	}
	cmd.AddCommand(migrateForceCmd(cfg))
	return cmd
}

func migrateForceCmd(cfg config) *cobra.Command {
	return &cobra.Command{
		Use:   "force <version>",
		Short: "Force migration version (use after a failed migration to reset dirty state)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			version, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid version %q: %w", args[0], err)
			}
			m, err := newMigrator(cfg)
			if err != nil {
				return err
			}
			defer m.Close()
			if err := m.Force(version); err != nil {
				return fmt.Errorf("force: %w", err)
			}
			fmt.Printf("Migration version forced to %d.\n", version)
			return nil
		},
	}
}
