package cmd

import (
	"github.com/skunkwerks/icy/pkg/access"
	"github.com/skunkwerks/icy/pkg/backend"
	"github.com/skunkwerks/icy/pkg/proto"
	"github.com/skunkwerks/icy/pkg/sshutils"
	"github.com/spf13/cobra"
)

// listCommand returns a command that lists the repositories the caller can
// read and proto.Visibility.Listed lets them see.
func listCommand() *cobra.Command {
	var all bool

	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List repositories",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			be := backend.FromContext(ctx)
			pk := sshutils.PublicKeyFromContext(ctx)
			user := proto.UserFromContext(ctx)
			signedIn := user != nil || isServerAdmin(ctx)
			repos, err := be.Repositories(ctx)
			if err != nil {
				return err
			}
			if all {
				cmd.PrintErrln("--all is deprecated: every repository you may see is listed now")
			}
			for _, r := range repos {
				var al access.AccessLevel
				if pk != nil {
					al = be.AccessLevelByPublicKey(ctx, r.Name(), pk)
				} else {
					al = be.AccessLevelForUser(ctx, r.Name(), user)
				}
				if al < access.ReadOnlyAccess {
					continue
				}
				if proto.VisibilityOf(r).Listed(signedIn) {
					cmd.Println(r.Name())
				}
			}
			return nil
		},
	}

	// --all used to show hidden repositories to anyone who could read
	// them, which let anonymous callers list unlisted ones. Every caller
	// now sees what Listed allows, so the flag does nothing.
	listCmd.Flags().BoolVarP(&all, "all", "a", false, "List all repositories")
	_ = listCmd.Flags().MarkHidden("all")

	return listCmd
}
