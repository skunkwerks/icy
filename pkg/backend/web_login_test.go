package backend

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/matryer/is"
	"github.com/skunkwerks/icy/pkg/proto"
)

// TestWebLoginIsSingleUse verifies a sign-in token works exactly once and
// that a used token gets the same answer as one that never existed.
func TestWebLoginIsSingleUse(t *testing.T) {
	is := is.New(t)
	be, _ := newTestBackend(t)
	ctx := context.Background()

	user, err := be.CreateUser(ctx, "alice", proto.UserOptions{})
	is.NoErr(err)

	token, err := be.CreateWebLogin(ctx, user, "/repo", time.Minute)
	is.NoErr(err)

	got, next, err := be.RedeemWebLogin(ctx, token)
	is.NoErr(err)
	is.Equal(got.ID(), user.ID())
	is.Equal(next, "/repo")

	_, _, err = be.RedeemWebLogin(ctx, token)
	is.True(errors.Is(err, ErrInvalidWebLogin))

	_, _, err = be.RedeemWebLogin(ctx, "icy_login_unknown")
	is.True(errors.Is(err, ErrInvalidWebLogin))
	_, _, err = be.RedeemWebLogin(ctx, "")
	is.True(errors.Is(err, ErrInvalidWebLogin))
}

// TestWebLoginExpires verifies an expired token is refused, and that the
// failed attempt also uses it up.
func TestWebLoginExpires(t *testing.T) {
	is := is.New(t)
	be, _ := newTestBackend(t)
	ctx := context.Background()

	user, err := be.CreateUser(ctx, "alice", proto.UserOptions{})
	is.NoErr(err)

	token, err := be.CreateWebLogin(ctx, user, "/", time.Millisecond)
	is.NoErr(err)
	time.Sleep(5 * time.Millisecond)

	_, _, err = be.RedeemWebLogin(ctx, token)
	is.True(errors.Is(err, ErrInvalidWebLogin))

	// Creating a new token prunes expired ones.
	_, err = be.CreateWebLogin(ctx, user, "/", time.Millisecond)
	is.NoErr(err)
	time.Sleep(5 * time.Millisecond)
	_, err = be.CreateWebLogin(ctx, user, "/", time.Minute)
	is.NoErr(err)
	be.webLogins.mu.Lock()
	is.Equal(len(be.webLogins.m), 1)
	be.webLogins.mu.Unlock()
}

// TestWebLoginDeletedUser verifies a token stops working when its user is
// deleted before it is used.
func TestWebLoginDeletedUser(t *testing.T) {
	is := is.New(t)
	be, _ := newTestBackend(t)
	ctx := context.Background()

	user, err := be.CreateUser(ctx, "alice", proto.UserOptions{})
	is.NoErr(err)
	token, err := be.CreateWebLogin(ctx, user, "/", time.Minute)
	is.NoErr(err)
	is.NoErr(be.DeleteUser(ctx, "alice"))

	_, _, err = be.RedeemWebLogin(ctx, token)
	is.True(errors.Is(err, ErrInvalidWebLogin))

	_, err = be.CreateWebLogin(ctx, nil, "/", time.Minute)
	is.True(err != nil)
}
