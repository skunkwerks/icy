package backend

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/matryer/is"
	"github.com/skunkwerks/icy/pkg/config"
	"github.com/skunkwerks/icy/pkg/db"
	"github.com/skunkwerks/icy/pkg/proto"
	"github.com/skunkwerks/icy/pkg/secrets"
	"github.com/skunkwerks/icy/pkg/store"
	"github.com/skunkwerks/icy/pkg/webhook"
)

// TestSecretsKeyLifecycle covers making the key on first start, refusing to
// replace a lost key that tokens depend on, and rotating it.
func TestSecretsKeyLifecycle(t *testing.T) {
	is := is.New(t)
	be, cfg := newTestBackend(t)
	ctx := context.Background()
	ctx = config.WithContext(ctx, cfg)
	ctx = db.WithContext(ctx, be.db)
	ctx = store.WithContext(ctx, be.store)
	path := secrets.KeyPath(cfg.DataPath)

	// Without a key, a token cannot be stored.
	repo, err := be.CreateRepository(ctx, "repo", nil, proto.RepositoryOptions{})
	is.NoErr(err)
	token := "tok"
	opts := WebhookOptions{BearerToken: &token}
	err = be.CreateWebhook(ctx, repo, "https://1.1.1.1/hook", webhook.ContentTypeJSON, "", []webhook.Event{webhook.EventPush}, true, opts)
	is.True(err != nil)

	// First start makes the key.
	is.NoErr(be.EnsureSecretsKey(ctx))
	key, err := secrets.LoadKey(path)
	is.NoErr(err)
	is.NoErr(be.CreateWebhook(ctx, repo, "https://1.1.1.1/hook", webhook.ContentTypeJSON, "", []webhook.Event{webhook.EventPush}, true, opts))
	is.NoErr(be.EnsureSecretsKey(ctx))

	// Rotation seals the token with a new key.
	n, err := be.RotateSecretsKey(ctx)
	is.NoErr(err)
	is.Equal(n, 1)
	newKey, err := secrets.LoadKey(path)
	is.NoErr(err)
	is.True(string(newKey) != string(key))
	hooks, err := be.store.ListWebhooksWithBearerToken(ctx, be.db)
	is.NoErr(err)
	is.Equal(len(hooks), 1)
	_, err = secrets.Decrypt(key, hooks[0].BearerToken)
	is.True(errors.Is(err, secrets.ErrDecrypt))
	plain, err := secrets.Decrypt(newKey, hooks[0].BearerToken)
	is.NoErr(err)
	is.Equal(string(plain), token)
	_, err = os.Stat(path + ".new")
	is.True(errors.Is(err, os.ErrNotExist))

	// A lost key is not silently replaced while tokens need it.
	is.NoErr(os.Remove(path))
	err = be.EnsureSecretsKey(ctx)
	is.True(errors.Is(err, secrets.ErrNoKey))
	_, err = os.Stat(path)
	is.True(errors.Is(err, os.ErrNotExist))
}
