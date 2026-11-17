package cmd

import (
	"github.com/skunkwerks/icy/pkg/access"
	"github.com/skunkwerks/icy/pkg/backend"
	"github.com/skunkwerks/icy/pkg/proto"
	"github.com/spf13/cobra"
)

// visibilityCommand gets or sets a repository's visibility. It is a view
// over the private and hidden flags, so it agrees with the private and
// hidden commands.
func visibilityCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "visibility REPOSITORY [public|unlisted|private]",
		Short: "Set or get a repository's visibility",
		Long: `Set or get a repository's visibility.

public    listed, and readable by anyone the anon-access setting allows
unlisted  not listed, but readable by anyone who knows the name
private   readable only by the owner, collaborators and admins`,
		Args:              cobra.RangeArgs(1, 2),
		PersistentPreRunE: requireRepo(access.ReadOnlyAccess),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			be := backend.FromContext(ctx)
			rr, _, err := repoFromContext(ctx)
			if err != nil {
				return err
			}

			if len(args) == 1 {
				cmd.Println(proto.VisibilityOf(rr))
				return nil
			}

			if err := requireLevel(ctx, access.ReadWriteAccess); err != nil {
				return err
			}
			v, err := proto.ParseVisibility(args[1])
			if err != nil {
				return err
			}
			return be.SetVisibility(ctx, rr.Name(), v)
		},
	}

	return cmd
}

// visibilityFlag resolves the --visibility flag of create and import into
// the private and hidden options. An empty value keeps the flags as given.
func visibilityFlag(value string, opts *proto.RepositoryOptions) error {
	if value == "" {
		return nil
	}
	v, err := proto.ParseVisibility(value)
	if err != nil {
		return err
	}
	opts.Private, opts.Hidden = v.Flags()
	return nil
}
