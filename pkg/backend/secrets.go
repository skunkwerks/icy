package backend

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/skunkwerks/icy/pkg/db"
	"github.com/skunkwerks/icy/pkg/secrets"
)

// secretsKeyPath returns the path of the key that seals webhook bearer
// tokens.
func (b *Backend) secretsKeyPath() string {
	return secrets.KeyPath(b.cfg.DataPath)
}

// EnsureSecretsKey creates the secrets key file in the data directory if it
// does not exist. It refuses to create one while webhooks hold tokens sealed
// with a key that has gone missing, since a new key could never open them.
// It logs a warning for tokens that the key on disk cannot open.
func (b *Backend) EnsureSecretsKey(ctx context.Context) error {
	path := b.secretsKeyPath()
	hooks, err := b.store.ListWebhooksWithBearerToken(ctx, b.db)
	if err != nil {
		return db.WrapError(err)
	}

	key, err := secrets.LoadKey(path)
	switch {
	case errors.Is(err, secrets.ErrNoKey) && len(hooks) > 0:
		return fmt.Errorf("%w, but %d webhooks have bearer tokens sealed with it: restore the key file from a backup, or clear the tokens", err, len(hooks))
	case errors.Is(err, secrets.ErrNoKey):
		if _, created, err := secrets.LoadOrCreateKey(path); err != nil {
			return err //nolint:wrapcheck
		} else if created {
			b.logger.Info("created secrets key", "path", path)
		}
		return nil
	case err != nil:
		return err //nolint:wrapcheck
	}

	for _, h := range hooks {
		if _, err := secrets.Decrypt(key, h.BearerToken); err != nil {
			b.logger.Warn("webhook bearer token cannot be opened with the secrets key", "webhook", h.ID, "path", path)
		}
	}
	return nil
}

// RotateSecretsKey seals every webhook bearer token with a new key, in one
// transaction, and then replaces the key file. It returns how many tokens it
// sealed again.
func (b *Backend) RotateSecretsKey(ctx context.Context) (int, error) {
	path := b.secretsKeyPath()
	oldKey, err := secrets.LoadKey(path)
	if err != nil {
		return 0, err //nolint:wrapcheck
	}
	newKey, err := secrets.NewKey()
	if err != nil {
		return 0, err //nolint:wrapcheck
	}

	// Write the new key next to the old one first, so it is safe on disk
	// before any token depends on it.
	next := path + ".new"
	if err := secrets.WriteKey(next, newKey); err != nil {
		return 0, err //nolint:wrapcheck
	}

	n := 0
	err = b.db.TransactionContext(ctx, func(tx *db.Tx) error {
		hooks, err := b.store.ListWebhooksWithBearerToken(ctx, tx)
		if err != nil {
			return db.WrapError(err)
		}
		for _, h := range hooks {
			token, err := secrets.Decrypt(oldKey, h.BearerToken)
			if err != nil {
				return fmt.Errorf("webhook %d: %w", h.ID, err)
			}
			sealed, err := secrets.Encrypt(newKey, token)
			if err != nil {
				return err //nolint:wrapcheck
			}
			if err := b.store.UpdateWebhookBearerToken(ctx, tx, h.RepoID, h.ID, sealed); err != nil {
				return db.WrapError(err)
			}
		}
		n = len(hooks)
		return nil
	})
	if err != nil {
		os.Remove(next) //nolint:errcheck
		return 0, err
	}

	if err := os.Rename(next, path); err != nil {
		return n, fmt.Errorf("tokens are sealed with the new key in %s, move it to %s: %w", next, path, err)
	}
	return n, nil
}
