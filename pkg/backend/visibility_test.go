package backend

import (
	"context"
	"errors"
	"testing"

	"github.com/matryer/is"
	"github.com/skunkwerks/icy/pkg/config"
	"github.com/skunkwerks/icy/pkg/db"
	"github.com/skunkwerks/icy/pkg/proto"
	"github.com/skunkwerks/icy/pkg/store"
)

// TestSetVisibility verifies each visibility maps onto the private and
// hidden flags and reads back the same.
func TestSetVisibility(t *testing.T) {
	is := is.New(t)
	be, cfg := newTestBackend(t)
	ctx := context.Background()
	ctx = config.WithContext(ctx, cfg)
	ctx = db.WithContext(ctx, be.db)
	ctx = store.WithContext(ctx, be.store)
	admin, err := be.User(ctx, "admin")
	is.NoErr(err)
	ctx = proto.WithUserContext(ctx, admin)

	_, err = be.CreateRepository(ctx, "repo", nil, proto.RepositoryOptions{Private: true, Hidden: true})
	is.NoErr(err)
	r, err := be.Repository(ctx, "repo")
	is.NoErr(err)
	is.Equal(proto.VisibilityOf(r), proto.VisibilityPrivate)

	for _, v := range []proto.Visibility{proto.VisibilityUnlisted, proto.VisibilityPublic, proto.VisibilityPrivate, proto.VisibilityUnlisted} {
		is.NoErr(be.SetVisibility(ctx, "repo", v))
		r, err := be.Repository(ctx, "repo")
		is.NoErr(err)
		is.Equal(proto.VisibilityOf(r), v)
		private, hidden := v.Flags()
		is.Equal(r.IsPrivate(), private)
		is.Equal(r.IsHidden(), hidden)
	}

	is.True(errors.Is(be.SetVisibility(ctx, "ghost", proto.VisibilityPublic), proto.ErrRepoNotFound))
}
