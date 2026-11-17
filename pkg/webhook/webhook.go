package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-querystring/query"
	"github.com/google/uuid"
	"github.com/skunkwerks/icy/git"
	"github.com/skunkwerks/icy/pkg/db"
	"github.com/skunkwerks/icy/pkg/db/models"
	"github.com/skunkwerks/icy/pkg/proto"
	"github.com/skunkwerks/icy/pkg/ssrf"
	"github.com/skunkwerks/icy/pkg/store"
	"github.com/skunkwerks/icy/pkg/utils"
)

// Hook is a repository webhook.
type Hook struct {
	models.Webhook
	ContentType ContentType
	Events      []Event
}

// Delivery is a webhook delivery.
type Delivery struct {
	models.WebhookDelivery
	Event Event
}

// Pending is a webhook delivery that is queued and not yet finished.
type Pending struct {
	models.WebhookQueueItem
	Event Event
}

// secureHTTPClient is an HTTP client with SSRF protection. Workers use it
// when the operator has not allowed any private networks.
var secureHTTPClient = ssrf.NewSecureClient()

// wake tells a worker running in this process that new work is queued, so
// events raised by the server itself go out at once. The git hook runs in a
// separate process, and the worker finds its deliveries by polling.
var wake = make(chan struct{}, 1)

func notify() {
	select {
	case wake <- struct{}{}:
	default:
	}
}

// render encodes a payload in the webhook's content type.
func render(contentType ContentType, payload any) (string, error) {
	switch contentType {
	case ContentTypeJSON:
		var b strings.Builder
		if err := json.NewEncoder(&b).Encode(payload); err != nil {
			return "", err //nolint:wrapcheck
		}
		return b.String(), nil
	case ContentTypeForm:
		v, err := query.Values(payload)
		if err != nil {
			return "", err //nolint:wrapcheck
		}
		return v.Encode(), nil
	default:
		return "", ErrInvalidContentType
	}
}

// enqueue writes a delivery to the queue under the delivery ID id, due now.
func enqueue(ctx context.Context, h db.Handler, datastore store.Store, id uuid.UUID, webhookID int64, event Event, contentType ContentType, body string) error {
	if err := datastore.CreateWebhookQueueItem(ctx, h, id, webhookID, int(event), int(contentType), body, time.Now().Unix()); err != nil {
		return db.WrapError(err)
	}
	return nil
}

// renderFor renders the body of one delivery for a webhook. It reports
// false when the webhook's format has nothing to send for this event, such
// as a tag push to a buildkite webhook.
func renderFor(w models.Webhook, id uuid.UUID, payload EventPayload) (string, ContentType, bool, error) {
	contentType := ContentType(w.ContentType) //nolint:gosec
	switch FormatOf(w.Format) {
	case FormatIcy:
		body, err := render(contentType, payload)
		return body, contentType, err == nil, err
	case FormatBuildkite:
		var push PushEvent
		switch p := payload.(type) {
		case PushEvent:
			push = p
		case *PushEvent:
			push = *p
		default:
			return "", 0, false, ErrFormatEvents
		}
		body, ok, err := buildkiteBody(push, id)
		return body, ContentTypeJSON, ok && err == nil, err
	default:
		return "", 0, false, ErrInvalidFormat
	}
}

// SendEvent queues an event for every active webhook of the repository that
// subscribes to it. It renders the payload now, in each webhook's format,
// and returns without waiting for the network: the server's webhook worker
// sends it and retries it.
//
// One webhook failing to queue does not stop the others. The errors are
// joined.
func SendEvent(ctx context.Context, payload EventPayload) error {
	dbx := db.FromContext(ctx)
	datastore := store.FromContext(ctx)
	webhooks, err := datastore.GetWebhooksByRepoIDWhereEvent(ctx, dbx, payload.RepositoryID(), []int{int(payload.Event())})
	if err != nil {
		return db.WrapError(err)
	}

	var errs []error
	queued := false
	for _, w := range webhooks {
		if !w.Active {
			continue
		}
		id, err := uuid.NewRandom()
		if err != nil {
			errs = append(errs, fmt.Errorf("webhook %d: %w", w.ID, err))
			continue
		}
		body, contentType, send, err := renderFor(w, id, payload)
		if err != nil {
			errs = append(errs, fmt.Errorf("webhook %d: %w", w.ID, err))
			continue
		}
		if !send {
			continue
		}
		if err := enqueue(ctx, dbx, datastore, id, w.ID, payload.Event(), contentType, body); err != nil {
			errs = append(errs, fmt.Errorf("webhook %d: %w", w.ID, err))
			continue
		}
		queued = true
	}
	if queued {
		notify()
	}

	return errors.Join(errs...)
}

// Redeliver queues a new delivery of a recorded one. It gets a new delivery
// ID and the request body is sent again unchanged.
func Redeliver(ctx context.Context, w models.Webhook, d models.WebhookDelivery) error {
	dbx := db.FromContext(ctx)
	datastore := store.FromContext(ctx)

	// The body was rendered in the content type of its day, so send it with
	// that type even if the webhook has changed since.
	contentType := ContentType(w.ContentType) //nolint:gosec
	for _, line := range strings.Split(d.RequestHeaders, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), "Content-Type") {
			if ct, err := ParseContentType(strings.TrimSpace(v)); err == nil {
				contentType = ct
			}
			break
		}
	}

	id, err := uuid.NewRandom()
	if err != nil {
		return err //nolint:wrapcheck
	}
	if err := enqueue(ctx, dbx, datastore, id, w.ID, Event(d.Event), contentType, d.RequestBody); err != nil {
		return err
	}
	notify()
	return nil
}

func repoURL(publicURL string, repo string) string {
	return fmt.Sprintf("%s/%s.git", publicURL, utils.SanitizeRepo(repo))
}

func getDefaultBranch(repo proto.Repository) (string, error) {
	branch, err := proto.RepositoryDefaultBranch(repo)
	// XXX: we check for ErrReferenceNotExist here because we don't want to
	// return an error if the repo is an empty repo.
	// This means that the repo doesn't have a default branch yet and this is
	// the first push to it.
	if err != nil && !errors.Is(err, git.ErrReferenceNotExist) {
		return "", err
	}

	return branch, nil
}
