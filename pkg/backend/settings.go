package backend

import (
	"context"

	"github.com/skunkwerks/icy/pkg/access"
	"github.com/skunkwerks/icy/pkg/db"
)

// AllowKeyless returns whether or not keyless access is allowed.
//
// If the server config sets AllowKeyless, that value always takes
// precedence over the database — it is a live override, not a one-time
// default, matching how InitialAdminKeys behaves. Do not cache this: a
// memoized/TTL'd copy would delay an admin's `settings allow-keyless false`
// from taking effect, which is a fail-open risk on an access-control check.
//
// With state_source "config" the database is read instead. The reconcile
// has already written the effective value there, and the startup copy in
// the config would hide a change made by a reload.
//
// It implements backend.Backend.
func (b *Backend) AllowKeyless(ctx context.Context) bool {
	if b.cfg.AllowKeyless != nil && !b.cfg.StateFromConfig() {
		return *b.cfg.AllowKeyless
	}

	var allow bool
	if err := b.db.TransactionContext(ctx, func(tx *db.Tx) error {
		var err error
		allow, err = b.store.GetAllowKeylessAccess(ctx, tx)
		return err
	}); err != nil {
		return false
	}

	return allow
}

// SetAllowKeyless sets whether or not keyless access is allowed.
//
// It implements backend.Backend.
func (b *Backend) SetAllowKeyless(ctx context.Context, allow bool) error {
	if err := b.checkStateWritable(); err != nil {
		return err
	}

	return b.db.TransactionContext(ctx, func(tx *db.Tx) error {
		return b.store.SetAllowKeylessAccess(ctx, tx, allow)
	})
}

// AnonAccess returns the level of anonymous access.
//
// If the server config sets AnonAccess, that value always takes precedence
// over the database — it is a live override, not a one-time default,
// matching how InitialAdminKeys behaves. Do not cache this: a memoized/TTL'd
// copy would delay an admin's `settings anon-access` change from taking
// effect, which is a fail-open risk on an access-control check.
//
// With state_source "config" the database is read instead, as for
// AllowKeyless.
//
// It implements backend.Backend.
func (b *Backend) AnonAccess(ctx context.Context) access.AccessLevel {
	if b.cfg.AnonAccess != nil && !b.cfg.StateFromConfig() {
		return *b.cfg.AnonAccess
	}

	var level access.AccessLevel
	if err := b.db.TransactionContext(ctx, func(tx *db.Tx) error {
		var err error
		level, err = b.store.GetAnonAccess(ctx, tx)
		return err
	}); err != nil {
		return access.NoAccess
	}

	return level
}

// SetAnonAccess sets the level of anonymous access.
//
// It implements backend.Backend.
func (b *Backend) SetAnonAccess(ctx context.Context, level access.AccessLevel) error {
	if err := b.checkStateWritable(); err != nil {
		return err
	}

	return b.db.TransactionContext(ctx, func(tx *db.Tx) error {
		return b.store.SetAnonAccess(ctx, tx, level)
	})
}
