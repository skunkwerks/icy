package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/matryer/is"
	"github.com/skunkwerks/icy/pkg/access"
	"github.com/skunkwerks/icy/pkg/proto"
)

// TestRepoListByRole pins who sees which repository in `repo list`. It is
// the same rule as the web front page and the TUI, see
// proto.Visibility.Listed:
//
//   - public repositories are listed for anyone who can read them
//   - unlisted repositories are listed only for signed in users
//   - private repositories are listed only for those who can read them,
//     even when they are also hidden
func TestRepoListByRole(t *testing.T) {
	is := is.New(t)
	ctx, be := newAuthTestContext(t)

	owner, err := be.CreateUser(ctx, "owner", proto.UserOptions{})
	is.NoErr(err)
	member, err := be.CreateUser(ctx, "member", proto.UserOptions{})
	is.NoErr(err)
	collab, err := be.CreateUser(ctx, "collab", proto.UserOptions{})
	is.NoErr(err)
	admin, err := be.CreateUser(ctx, "boss", proto.UserOptions{Admin: true})
	is.NoErr(err)

	for _, r := range []struct {
		name string
		opts proto.RepositoryOptions
	}{
		{"pub", proto.RepositoryOptions{}},
		{"unl", proto.RepositoryOptions{Hidden: true}},
		{"priv", proto.RepositoryOptions{Private: true}},
		{"privhid", proto.RepositoryOptions{Private: true, Hidden: true}},
	} {
		_, err := be.CreateRepository(ctx, r.name, owner, r.opts)
		is.NoErr(err)
	}
	is.NoErr(be.AddCollaborator(proto.WithUserContext(ctx, owner), "priv", "collab", access.ReadOnlyAccess))

	cases := []struct {
		role string
		ctx  context.Context
		want []string
	}{
		{"anonymous", ctx, []string{"pub"}},
		{"member", proto.WithUserContext(ctx, member), []string{"pub", "unl"}},
		{"collaborator", proto.WithUserContext(ctx, collab), []string{"priv", "pub", "unl"}},
		{"owner", proto.WithUserContext(ctx, owner), []string{"priv", "privhid", "pub", "unl"}},
		{"admin", proto.WithUserContext(ctx, admin), []string{"priv", "privhid", "pub", "unl"}},
	}
	for _, c := range cases {
		t.Run(c.role, func(t *testing.T) {
			is := is.New(t)
			out, _, err := runRepoOutput(t, c.ctx, "list")
			is.NoErr(err)
			is.Equal(sortedLines(out), c.want)
		})
	}

	// --all no longer shows unlisted repositories to anonymous callers.
	out, stderr, err := runRepoOutput(t, ctx, "list", "--all")
	is.NoErr(err)
	is.Equal(sortedLines(out), []string{"pub"})
	is.True(strings.Contains(stderr, "deprecated"))
}

func sortedLines(s string) []string {
	lines := strings.Fields(s)
	for i := range lines {
		for j := i + 1; j < len(lines); j++ {
			if lines[j] < lines[i] {
				lines[i], lines[j] = lines[j], lines[i]
			}
		}
	}
	return lines
}
