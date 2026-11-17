package admin

import (
	"fmt"

	"github.com/skunkwerks/icy/cmd"
	"github.com/skunkwerks/icy/pkg/backend"
	"github.com/skunkwerks/icy/pkg/config"
	"github.com/skunkwerks/icy/pkg/statefile"
	"github.com/spf13/cobra"
)

// exportStateCmd prints the users, repositories and access settings in the
// database as config.yaml keys, so that a server can move to
// state_source: config.
func exportStateCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "export-state",
		Short:              "Print the database's users, repositories and access as config.yaml keys",
		Args:               cobra.NoArgs,
		PersistentPreRunE:  cmd.InitBackendContext,
		PersistentPostRunE: cmd.CloseDBContext,
		RunE: func(c *cobra.Command, _ []string) error {
			ctx := c.Context()
			st, err := backend.FromContext(ctx).ExportState(ctx)
			if err != nil {
				return err
			}
			return st.File().Write(c.OutOrStdout())
		},
	}
}

// checkStateCmd checks the users, repos and access settings in a
// config.yaml without touching the database. It suits a config management
// tool's validate step.
func checkStateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check-state [FILE]",
		Short: "Check the users, repos and access settings in a config.yaml",
		Long: "Check the users, repos and access settings in a config.yaml for errors, " +
			"as the server would with state_source set to \"config\". " +
			"FILE defaults to the server's config.yaml.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			path := config.FromContext(c.Context()).ConfigPath()
			if len(args) == 1 {
				path = args[0]
			}

			// Read the whole file as the server would, so that its own
			// initial_admin_keys count towards the lockout check.
			cfg := config.DefaultConfig()
			if err := cfg.ParseFileFrom(path); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			if err := cfg.ParseEnv(); err != nil {
				return err
			}

			opts, err := statefile.OptionsFor(cfg)
			if err != nil {
				return err
			}
			st, err := statefile.Load(path, opts)
			if err != nil {
				return err
			}

			fmt.Fprintf(c.OutOrStdout(), "%s: %d users, %d repositories\n", path, len(st.Users), len(st.Repos))
			return nil
		},
	}
}
