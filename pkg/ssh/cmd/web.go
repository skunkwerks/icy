package cmd

import (
	"net/url"

	"github.com/skunkwerks/icy/pkg/backend"
	"github.com/skunkwerks/icy/pkg/config"
	"github.com/skunkwerks/icy/pkg/proto"
	"github.com/skunkwerks/icy/pkg/utils"
	"github.com/spf13/cobra"
)

// WebCommand returns the command for the web UI.
func WebCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Sign in to the web UI",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "login [REPOSITORY]",
		Short: "Print a single-use link that signs you in to the web UI",
		Long: `Print a single-use link that signs you in to the web UI.

The link works once and expires after five minutes. Opening it gives your
browser a session cookie with your read access. When REPOSITORY is given,
the link opens that repository after signing in.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg := config.FromContext(ctx)
			be := backend.FromContext(ctx)

			user := proto.UserFromContext(ctx)
			if user == nil {
				return proto.ErrUserNotFound
			}

			// The landing page is fixed here, on the trusted side, so the
			// sign-in request cannot redirect anywhere else. It is not
			// checked against the user's access: the web UI does that, and
			// checking here would tell the caller which names exist.
			next := "/"
			if len(args) == 1 {
				if name := utils.SanitizeRepo(args[0]); name != "" && utils.ValidateRepo(name) == nil {
					next = "/" + (&url.URL{Path: name}).EscapedPath()
				}
			}

			token, err := be.CreateWebLogin(ctx, user, next, backend.WebLoginTTL)
			if err != nil {
				return err
			}

			cmd.PrintErrln("This link signs you in once and expires in 5 minutes:")
			cmd.Println(cfg.HTTP.PublicURL + "/~/login?token=" + url.QueryEscape(token))
			return nil
		},
	})

	return cmd
}
