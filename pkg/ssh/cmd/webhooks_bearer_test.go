package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/matryer/is"
	"github.com/skunkwerks/icy/pkg/backend"
	"github.com/skunkwerks/icy/pkg/config"
	"github.com/skunkwerks/icy/pkg/db"
	"github.com/skunkwerks/icy/pkg/proto"
	"github.com/skunkwerks/icy/pkg/secrets"
	"github.com/skunkwerks/icy/pkg/store"
	"github.com/skunkwerks/icy/pkg/webhook"
)

// runRepoStdin runs a `repo` subcommand with stdin and returns its stdout
// and error.
func runRepoStdin(t *testing.T, ctx context.Context, stdin string, args ...string) (string, error) {
	t.Helper()
	c := RepoCommand()
	c.SilenceUsage = true
	var outBuf, errBuf bytes.Buffer
	c.SetIn(strings.NewReader(stdin))
	c.SetOut(&outBuf)
	c.SetErr(&errBuf)
	c.SetArgs(args)
	err := c.ExecuteContext(ctx)
	return outBuf.String(), err
}

// newWebhookTestContext is newAuthTestContext with a secrets key, as icy
// serve makes on start, and loopback in webhook.allowed_networks.
func newWebhookTestContext(t *testing.T) (context.Context, *backend.Backend) {
	t.Helper()
	ctx, be := newAuthTestContext(t)
	cfg := config.FromContext(ctx)
	cfg.Webhook.AllowedNetworks = []string{"127.0.0.1"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := be.EnsureSecretsKey(ctx); err != nil {
		t.Fatal(err)
	}
	return ctx, be
}

// storedWebhook reads a webhook row straight from the database.
func storedWebhook(t *testing.T, ctx context.Context, repo proto.Repository, id int64) webhook.Hook {
	t.Helper()
	h, err := store.FromContext(ctx).GetWebhookByID(ctx, db.FromContext(ctx), repo.ID(), id)
	if err != nil {
		t.Fatal(err)
	}
	return webhook.Hook{Webhook: h}
}

func TestWebhookBearerTokenServerAdminOnly(t *testing.T) {
	is := is.New(t)
	ctx, be := newWebhookTestContext(t)

	adminCtx := withUser(t, ctx, be, "root", true)
	ownerCtx := withUser(t, ctx, be, "owner", false)
	owner := proto.UserFromContext(ownerCtx)
	repo, err := be.CreateRepository(ownerCtx, "app", owner, proto.RepositoryOptions{Private: true})
	is.NoErr(err)

	const token = "bkua_sup3r-s3cret"
	const hookURL = "https://1.1.1.1/hook"

	// The repo admin may not create a webhook with a token.
	_, err = runRepoStdin(t, ownerCtx, token+"\n", "webhook", "create", "app", hookURL, "-e", "push", "--bearer-stdin")
	is.True(errors.Is(err, proto.ErrUnauthorized))
	hooks, err := be.ListWebhooks(ctx, repo)
	is.NoErr(err)
	is.Equal(len(hooks), 0)

	// A plain webhook is fine.
	_, err = runRepoStdin(t, ownerCtx, "", "webhook", "create", "app", hookURL, "-e", "push")
	is.NoErr(err)
	hooks, err = be.ListWebhooks(ctx, repo)
	is.NoErr(err)
	is.Equal(len(hooks), 1)
	id := hooks[0].ID

	// Nor set or clear a token on it.
	_, err = runRepoStdin(t, ownerCtx, token, "webhook", "update", "app", "1", "--bearer-stdin")
	is.True(errors.Is(err, proto.ErrUnauthorized))
	_, err = runRepoStdin(t, ownerCtx, "", "webhook", "update", "app", "1", "--no-bearer")
	is.True(errors.Is(err, proto.ErrUnauthorized))
	is.Equal(storedWebhook(t, ctx, repo, id).BearerToken, "")

	// A server admin can. One trailing newline is trimmed.
	_, err = runRepoStdin(t, adminCtx, token+"\n", "webhook", "update", "app", "1", "--bearer-stdin")
	is.NoErr(err)
	sealed := storedWebhook(t, ctx, repo, id).BearerToken
	is.True(strings.HasPrefix(sealed, "v1:"))
	is.True(!strings.Contains(sealed, token))
	key, err := secrets.LoadKey(secrets.KeyPath(config.FromContext(ctx).DataPath))
	is.NoErr(err)
	plain, err := secrets.Decrypt(key, sealed)
	is.NoErr(err)
	is.Equal(string(plain), token)

	// list and info say a token is set, and never show it.
	for _, c := range []context.Context{ownerCtx, adminCtx} {
		for _, args := range [][]string{
			{"webhook", "list", "app"},
			{"webhook", "info", "app", "1"},
		} {
			out, err := runRepoStdin(t, c, "", args...)
			is.NoErr(err)
			if !strings.Contains(out, "bearer (set)") {
				t.Errorf("%v: output does not say a token is set: %q", args, out)
			}
			if strings.Contains(out, token) || strings.Contains(out, sealed) || strings.Contains(out, "v1:") {
				t.Errorf("%v: output shows the token: %q", args, out)
			}
		}
	}

	// The repo admin can still edit the rest, and the token stays.
	_, err = runRepoStdin(t, ownerCtx, "", "webhook", "update", "app", "1", "--active", "false")
	is.NoErr(err)
	is.Equal(storedWebhook(t, ctx, repo, id).BearerToken, sealed)

	// But cannot move the webhook, which would send the token elsewhere.
	_, err = runRepoStdin(t, ownerCtx, "", "webhook", "update", "app", "1", "--url", "https://8.8.8.8/steal")
	is.True(errors.Is(err, proto.ErrUnauthorized))
	is.Equal(storedWebhook(t, ctx, repo, id).URL, hookURL)
	_, err = runRepoStdin(t, ownerCtx, "", "webhook", "update", "app", "1", "--url", hookURL)
	is.NoErr(err)

	// --no-bearer clears it.
	_, err = runRepoStdin(t, adminCtx, "", "webhook", "update", "app", "1", "--no-bearer")
	is.NoErr(err)
	is.Equal(storedWebhook(t, ctx, repo, id).BearerToken, "")
	out, err := runRepoStdin(t, ownerCtx, "", "webhook", "info", "app", "1")
	is.NoErr(err)
	is.True(!strings.Contains(out, "bearer"))

	// Both flags at once make no sense.
	_, err = runRepoStdin(t, adminCtx, token, "webhook", "update", "app", "1", "--bearer-stdin", "--no-bearer")
	is.True(err != nil)

	// An empty token is refused.
	_, err = runRepoStdin(t, adminCtx, "\n", "webhook", "update", "app", "1", "--bearer-stdin")
	is.True(errors.Is(err, webhook.ErrInvalidBearerToken))
}

func TestWebhookBearerTokenNeedsHTTPS(t *testing.T) {
	is := is.New(t)
	ctx, be := newWebhookTestContext(t)
	adminCtx := withUser(t, ctx, be, "root", true)
	repo, err := be.CreateRepository(adminCtx, "app", nil, proto.RepositoryOptions{})
	is.NoErr(err)

	// Plain http to a public address would send the token in clear text.
	_, err = runRepoStdin(t, adminCtx, "tok", "webhook", "create", "app", "http://1.1.1.1/hook", "-e", "push", "--bearer-stdin")
	is.True(errors.Is(err, webhook.ErrBearerNeedsHTTPS))

	// It is allowed to an address in webhook.allowed_networks.
	_, err = runRepoStdin(t, adminCtx, "tok", "webhook", "create", "app", "http://127.0.0.1:9/hook", "-e", "push", "--bearer-stdin")
	is.NoErr(err)

	// Moving a webhook that has a token to plain http is refused too.
	_, err = runRepoStdin(t, adminCtx, "", "webhook", "update", "app", "1", "--url", "http://1.1.1.1/hook")
	is.True(errors.Is(err, webhook.ErrBearerNeedsHTTPS))

	// Setting a token on a plain http webhook is refused.
	_, err = runRepoStdin(t, adminCtx, "", "webhook", "create", "app", "http://1.1.1.1/other", "-e", "push")
	is.NoErr(err)
	_, err = runRepoStdin(t, adminCtx, "tok", "webhook", "update", "app", "2", "--bearer-stdin")
	is.True(errors.Is(err, webhook.ErrBearerNeedsHTTPS))
	is.Equal(storedWebhook(t, ctx, repo, 2).BearerToken, "")
}

func TestWebhookBuildkiteFormat(t *testing.T) {
	is := is.New(t)
	ctx, be := newWebhookTestContext(t)
	adminCtx := withUser(t, ctx, be, "root", true)
	repo, err := be.CreateRepository(adminCtx, "app", nil, proto.RepositoryOptions{})
	is.NoErr(err)

	const bk = "https://1.1.1.1/v2/organizations/o/pipelines/p/builds"

	for _, args := range [][]string{
		{"-e", "push", "-e", "branch_tag_create"},
		{"-e", "collaborator"},
		{"-e", "push", "--content-type", "form"},
	} {
		_, err := runRepoStdin(t, adminCtx, "", append([]string{"webhook", "create", "app", bk, "--format", "buildkite"}, args...)...)
		if err == nil {
			t.Errorf("buildkite webhook with %v was accepted", args)
		}
	}
	_, err = runRepoStdin(t, adminCtx, "", "webhook", "create", "app", bk, "-e", "push", "--format", "gitlab")
	is.True(errors.Is(err, webhook.ErrInvalidFormat))

	_, err = runRepoStdin(t, adminCtx, "tok", "webhook", "create", "app", bk, "-e", "push", "--format", "buildkite", "--bearer-stdin")
	is.NoErr(err)
	h := storedWebhook(t, ctx, repo, 1)
	is.Equal(webhook.FormatOf(h.Format), webhook.FormatBuildkite)

	// Adding another event to it is refused.
	_, err = runRepoStdin(t, adminCtx, "", "webhook", "update", "app", "1", "-e", "push,collaborator")
	is.True(errors.Is(err, webhook.ErrFormatEvents))

	// So is switching a webhook with other events to buildkite.
	_, err = runRepoStdin(t, adminCtx, "", "webhook", "create", "app", "https://1.1.1.1/other", "-e", "collaborator")
	is.NoErr(err)
	_, err = runRepoStdin(t, adminCtx, "", "webhook", "update", "app", "2", "--format", "buildkite")
	is.True(errors.Is(err, webhook.ErrFormatEvents))
	is.Equal(webhook.FormatOf(storedWebhook(t, ctx, repo, 2).Format), webhook.FormatIcy)

	out, err := runRepoStdin(t, adminCtx, "", "webhook", "info", "app", "1")
	is.NoErr(err)
	is.True(strings.Contains(out, "Format: buildkite"))
}
