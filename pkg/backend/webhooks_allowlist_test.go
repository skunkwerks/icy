package backend

import (
	"context"
	"errors"
	"testing"

	"github.com/matryer/is"
	"github.com/skunkwerks/icy/pkg/config"
	"github.com/skunkwerks/icy/pkg/db"
	"github.com/skunkwerks/icy/pkg/proto"
	"github.com/skunkwerks/icy/pkg/ssrf"
	"github.com/skunkwerks/icy/pkg/store"
	"github.com/skunkwerks/icy/pkg/webhook"
)

// TestWebhookAllowedNetworks verifies that webhook.allowed_networks opens
// exactly the listed private addresses when a webhook is created or
// updated, and nothing else.
func TestWebhookAllowedNetworks(t *testing.T) {
	is := is.New(t)
	be, cfg := newTestBackend(t)
	cfg.Webhook.AllowedNetworks = []string{"10.1.0.0/16", "192.168.5.7"}
	is.NoErr(cfg.Validate())

	ctx := context.Background()
	ctx = config.WithContext(ctx, cfg)
	ctx = db.WithContext(ctx, be.db)
	ctx = store.WithContext(ctx, be.store)
	admin, err := be.User(ctx, "admin")
	is.NoErr(err)
	ctx = proto.WithUserContext(ctx, admin)

	repo, err := be.CreateRepository(ctx, "repo", nil, proto.RepositoryOptions{})
	is.NoErr(err)

	create := func(url string) error {
		return be.CreateWebhook(ctx, repo, url, webhook.ContentTypeJSON, "", []webhook.Event{webhook.EventPush}, true)
	}

	for _, url := range []string{
		"http://10.1.2.3:8080/hook",
		"http://[::ffff:10.1.9.9]/hook",
		"https://192.168.5.7/hook",
	} {
		if err := create(url); err != nil {
			t.Errorf("create %s: %v", url, err)
		}
	}

	for _, url := range []string{
		"http://10.2.0.1/hook",
		"http://192.168.5.8/hook",
		"http://127.0.0.1/hook",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::ffff:127.0.0.1]/hook",
		"http://localhost/hook",
	} {
		if err := create(url); !errors.Is(err, ssrf.ErrPrivateIP) {
			t.Errorf("create %s = %v, want ErrPrivateIP", url, err)
		}
	}

	hooks, err := be.ListWebhooks(ctx, repo)
	is.NoErr(err)
	is.Equal(len(hooks), 3)

	id := hooks[0].ID
	update := func(url string) error {
		return be.UpdateWebhook(ctx, repo, id, url, webhook.ContentTypeJSON, "", []webhook.Event{webhook.EventPush}, true)
	}
	is.NoErr(update("http://10.1.200.1/hook"))
	if err := update("http://10.3.0.1/hook"); !errors.Is(err, ssrf.ErrPrivateIP) {
		t.Errorf("update outside allowlist = %v, want ErrPrivateIP", err)
	}

	// Without the allowlist the same address is refused again.
	cfg.Webhook.AllowedNetworks = nil
	is.NoErr(cfg.Validate())
	if err := create("http://10.1.2.4/hook"); !errors.Is(err, ssrf.ErrPrivateIP) {
		t.Errorf("create without allowlist = %v, want ErrPrivateIP", err)
	}
}
