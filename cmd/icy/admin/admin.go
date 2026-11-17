package admin

import (
	"fmt"

	"github.com/skunkwerks/icy/cmd"
	"github.com/skunkwerks/icy/pkg/backend"
	"github.com/skunkwerks/icy/pkg/config"
	"github.com/skunkwerks/icy/pkg/db"
	"github.com/skunkwerks/icy/pkg/db/migrate"
	"github.com/spf13/cobra"
)

var (
	// Command is the admin command.
	Command = &cobra.Command{
		Use:   "admin",
		Short: "Administrate the server",
	}

	migrateCmd = &cobra.Command{
		Use:                "migrate",
		Short:              "Migrate the database to the latest version",
		PersistentPreRunE:  cmd.InitBackendContext,
		PersistentPostRunE: cmd.CloseDBContext,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			db := db.FromContext(ctx)
			if err := migrate.Migrate(ctx, db); err != nil {
				return fmt.Errorf("migration: %w", err)
			}

			return nil
		},
	}

	rollbackCmd = &cobra.Command{
		Use:                "rollback",
		Short:              "Rollback the database to the previous version",
		PersistentPreRunE:  cmd.InitBackendContext,
		PersistentPostRunE: cmd.CloseDBContext,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			db := db.FromContext(ctx)
			if err := migrate.Rollback(ctx, db); err != nil {
				return fmt.Errorf("rollback: %w", err)
			}

			return nil
		},
	}

	syncHooksCmd = &cobra.Command{
		Use:                "sync-hooks",
		Short:              "Update repository hooks",
		PersistentPreRunE:  cmd.InitBackendContext,
		PersistentPostRunE: cmd.CloseDBContext,
		RunE: func(c *cobra.Command, _ []string) error {
			ctx := c.Context()
			cfg := config.FromContext(ctx)
			be := backend.FromContext(ctx)
			if err := cmd.InitializeHooks(ctx, cfg, be); err != nil {
				return fmt.Errorf("initialize hooks: %w", err)
			}

			return nil
		},
	}

	rotateSecretsKeyCmd = &cobra.Command{
		Use:                "rotate-secrets-key",
		Short:              "Seal webhook bearer tokens with a new secrets key",
		Args:               cobra.NoArgs,
		PersistentPreRunE:  cmd.InitBackendContext,
		PersistentPostRunE: cmd.CloseDBContext,
		RunE: func(c *cobra.Command, _ []string) error {
			ctx := c.Context()
			be := backend.FromContext(ctx)
			n, err := be.RotateSecretsKey(ctx)
			if err != nil {
				return fmt.Errorf("rotate secrets key: %w", err)
			}
			c.Printf("Sealed %d bearer tokens with the new key.\n", n)
			return nil
		},
	}
)

func init() {
	Command.AddCommand(
		exportStateCmd(),
		checkStateCmd(),
		syncHooksCmd,
		rotateSecretsKeyCmd,
		migrateCmd,
		rollbackCmd,
	)
}
