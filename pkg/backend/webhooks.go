package backend

import (
	"context"

	"github.com/google/uuid"
	"github.com/skunkwerks/icy/pkg/db"
	"github.com/skunkwerks/icy/pkg/db/models"
	"github.com/skunkwerks/icy/pkg/proto"
	"github.com/skunkwerks/icy/pkg/ssrf"
	"github.com/skunkwerks/icy/pkg/store"
	"github.com/skunkwerks/icy/pkg/utils"
	"github.com/skunkwerks/icy/pkg/webhook"
)

// webhookAllowlist returns the private networks the operator lets webhooks
// reach.
func (b *Backend) webhookAllowlist() ssrf.AllowedNetworks {
	if b.cfg == nil {
		return nil
	}
	return b.cfg.Webhook.Allowlist()
}

// CreateWebhook creates a webhook for a repository.
func (b *Backend) CreateWebhook(ctx context.Context, repo proto.Repository, url string, contentType webhook.ContentType, secret string, events []webhook.Event, active bool) error {
	dbx := db.FromContext(ctx)
	datastore := store.FromContext(ctx)
	url = utils.Sanitize(url)

	// Validate webhook URL to prevent SSRF attacks
	if err := webhook.ValidateWebhookURLWithAllowlist(url, b.webhookAllowlist()); err != nil {
		return err //nolint:wrapcheck
	}

	return dbx.TransactionContext(ctx, func(tx *db.Tx) error {
		lastID, err := datastore.CreateWebhook(ctx, tx, repo.ID(), url, secret, int(contentType), active)
		if err != nil {
			return db.WrapError(err)
		}

		evs := make([]int, len(events))
		for i, e := range events {
			evs[i] = int(e)
		}
		if err := datastore.CreateWebhookEvents(ctx, tx, lastID, evs); err != nil {
			return db.WrapError(err)
		}

		return nil
	})
}

// Webhook returns a webhook for a repository.
func (b *Backend) Webhook(ctx context.Context, repo proto.Repository, id int64) (webhook.Hook, error) {
	dbx := db.FromContext(ctx)
	datastore := store.FromContext(ctx)

	var wh webhook.Hook
	if err := dbx.TransactionContext(ctx, func(tx *db.Tx) error {
		h, err := datastore.GetWebhookByID(ctx, tx, repo.ID(), id)
		if err != nil {
			return db.WrapError(err)
		}
		events, err := datastore.GetWebhookEventsByWebhookID(ctx, tx, id)
		if err != nil {
			return db.WrapError(err)
		}

		wh = webhook.Hook{
			Webhook:     h,
			ContentType: webhook.ContentType(h.ContentType), //nolint:gosec
			Events:      make([]webhook.Event, len(events)),
		}
		for i, e := range events {
			wh.Events[i] = webhook.Event(e.Event)
		}

		return nil
	}); err != nil {
		return webhook.Hook{}, db.WrapError(err)
	}

	return wh, nil
}

// ListWebhooks lists webhooks for a repository.
func (b *Backend) ListWebhooks(ctx context.Context, repo proto.Repository) ([]webhook.Hook, error) {
	dbx := db.FromContext(ctx)
	datastore := store.FromContext(ctx)

	var webhooks []models.Webhook
	webhookEvents := map[int64][]models.WebhookEvent{}
	if err := dbx.TransactionContext(ctx, func(tx *db.Tx) error {
		var err error
		webhooks, err = datastore.GetWebhooksByRepoID(ctx, tx, repo.ID())
		if err != nil {
			return err
		}

		for _, h := range webhooks {
			events, err := datastore.GetWebhookEventsByWebhookID(ctx, tx, h.ID)
			if err != nil {
				return err
			}
			webhookEvents[h.ID] = events
		}

		return nil
	}); err != nil {
		return nil, db.WrapError(err)
	}

	hooks := make([]webhook.Hook, len(webhooks))
	for i, h := range webhooks {
		events := make([]webhook.Event, len(webhookEvents[h.ID]))
		for i, e := range webhookEvents[h.ID] {
			events[i] = webhook.Event(e.Event)
		}

		hooks[i] = webhook.Hook{
			Webhook:     h,
			ContentType: webhook.ContentType(h.ContentType), //nolint:gosec
			Events:      events,
		}
	}

	return hooks, nil
}

// UpdateWebhook updates a webhook.
func (b *Backend) UpdateWebhook(ctx context.Context, repo proto.Repository, id int64, url string, contentType webhook.ContentType, secret string, updatedEvents []webhook.Event, active bool) error {
	dbx := db.FromContext(ctx)
	datastore := store.FromContext(ctx)

	// Validate webhook URL to prevent SSRF attacks
	if err := webhook.ValidateWebhookURLWithAllowlist(url, b.webhookAllowlist()); err != nil {
		return err
	}

	return dbx.TransactionContext(ctx, func(tx *db.Tx) error {
		if err := datastore.UpdateWebhookByID(ctx, tx, repo.ID(), id, url, secret, int(contentType), active); err != nil {
			return db.WrapError(err)
		}

		currentEvents, err := datastore.GetWebhookEventsByWebhookID(ctx, tx, id)
		if err != nil {
			return db.WrapError(err)
		}

		// Delete events that are no longer in the list.
		toBeDeleted := make([]int64, 0)
		for _, e := range currentEvents {
			found := false
			for _, ne := range updatedEvents {
				if int(ne) == e.Event {
					found = true
					break
				}
			}
			if !found {
				toBeDeleted = append(toBeDeleted, e.ID)
			}
		}

		if err := datastore.DeleteWebhookEventsByID(ctx, tx, toBeDeleted); err != nil {
			return db.WrapError(err)
		}

		// Prune events that are already in the list.
		newEvents := make([]int, 0)
		for _, e := range updatedEvents {
			found := false
			for _, ne := range currentEvents {
				if int(e) == ne.Event {
					found = true
					break
				}
			}
			if !found {
				newEvents = append(newEvents, int(e))
			}
		}

		if err := datastore.CreateWebhookEvents(ctx, tx, id, newEvents); err != nil {
			return db.WrapError(err)
		}

		return nil
	})
}

// DeleteWebhook deletes a webhook for a repository.
func (b *Backend) DeleteWebhook(ctx context.Context, repo proto.Repository, id int64) error {
	dbx := db.FromContext(ctx)
	datastore := store.FromContext(ctx)

	return dbx.TransactionContext(ctx, func(tx *db.Tx) error {
		_, err := datastore.GetWebhookByID(ctx, tx, repo.ID(), id)
		if err != nil {
			return db.WrapError(err)
		}
		if err := datastore.DeleteWebhookForRepoByID(ctx, tx, repo.ID(), id); err != nil {
			return db.WrapError(err)
		}

		return nil
	})
}

// ListWebhookDeliveries lists webhook deliveries for a webhook belonging to
// the given repository.
//
// The webhook is resolved scoped to the repository, so a caller authorized
// only for repo A cannot read deliveries of a webhook owned by repo B.
func (b *Backend) ListWebhookDeliveries(ctx context.Context, repo proto.Repository, id int64) ([]webhook.Delivery, error) {
	dbx := db.FromContext(ctx)
	datastore := store.FromContext(ctx)

	var deliveries []models.WebhookDelivery
	if err := dbx.TransactionContext(ctx, func(tx *db.Tx) error {
		if _, err := datastore.GetWebhookByID(ctx, tx, repo.ID(), id); err != nil {
			return db.WrapError(err)
		}

		var err error
		deliveries, err = datastore.ListWebhookDeliveriesByWebhookID(ctx, tx, id)
		if err != nil {
			return db.WrapError(err)
		}

		return nil
	}); err != nil {
		return nil, db.WrapError(err)
	}

	ds := make([]webhook.Delivery, len(deliveries))
	for i, d := range deliveries {
		ds[i] = webhook.Delivery{
			WebhookDelivery: d,
			Event:           webhook.Event(d.Event),
		}
	}

	return ds, nil
}

// RedeliverWebhookDelivery queues a recorded delivery to be sent again. The
// webhook worker in the server sends it.
func (b *Backend) RedeliverWebhookDelivery(ctx context.Context, repo proto.Repository, id int64, delID uuid.UUID) error {
	dbx := db.FromContext(ctx)
	datastore := store.FromContext(ctx)

	var delivery models.WebhookDelivery
	var wh models.Webhook
	if err := dbx.TransactionContext(ctx, func(tx *db.Tx) error {
		var err error
		wh, err = datastore.GetWebhookByID(ctx, tx, repo.ID(), id)
		if err != nil {
			return db.WrapError(err)
		}

		delivery, err = datastore.GetWebhookDeliveryByID(ctx, tx, id, delID)
		if err != nil {
			return db.WrapError(err)
		}

		return nil
	}); err != nil {
		return db.WrapError(err)
	}

	b.logger.Info("redelivering webhook delivery", "webhook", id, "delivery", delID)

	return webhook.Redeliver(ctx, wh, delivery)
}

// ListPendingWebhookDeliveries lists the queued deliveries of a webhook
// belonging to the given repository: those not yet sent, and those waiting
// for a retry.
func (b *Backend) ListPendingWebhookDeliveries(ctx context.Context, repo proto.Repository, id int64) ([]webhook.Pending, error) {
	dbx := db.FromContext(ctx)
	datastore := store.FromContext(ctx)

	var items []models.WebhookQueueItem
	if err := dbx.TransactionContext(ctx, func(tx *db.Tx) error {
		if _, err := datastore.GetWebhookByID(ctx, tx, repo.ID(), id); err != nil {
			return db.WrapError(err)
		}

		var err error
		items, err = datastore.ListWebhookQueueItemsByWebhookID(ctx, tx, id)
		return db.WrapError(err)
	}); err != nil {
		return nil, db.WrapError(err)
	}

	ps := make([]webhook.Pending, len(items))
	for i, it := range items {
		ps[i] = webhook.Pending{WebhookQueueItem: it, Event: webhook.Event(it.Event)}
	}
	return ps, nil
}

// WebhookDelivery returns a webhook delivery for a webhook belonging to the
// given repository.
//
// The webhook is resolved scoped to the repository, so a caller authorized
// only for repo A cannot read a delivery of a webhook owned by repo B.
// Deliveries expose the full request URL, headers, and body, so this scoping
// is what keeps them from leaking across repositories.
func (b *Backend) WebhookDelivery(ctx context.Context, repo proto.Repository, webhookID int64, id uuid.UUID) (webhook.Delivery, error) {
	dbx := db.FromContext(ctx)
	datastore := store.FromContext(ctx)

	var delivery webhook.Delivery
	if err := dbx.TransactionContext(ctx, func(tx *db.Tx) error {
		if _, err := datastore.GetWebhookByID(ctx, tx, repo.ID(), webhookID); err != nil {
			return db.WrapError(err)
		}

		d, err := datastore.GetWebhookDeliveryByID(ctx, tx, webhookID, id)
		if err != nil {
			return db.WrapError(err)
		}

		delivery = webhook.Delivery{
			WebhookDelivery: d,
			Event:           webhook.Event(d.Event),
		}

		return nil
	}); err != nil {
		return webhook.Delivery{}, db.WrapError(err)
	}

	return delivery, nil
}
