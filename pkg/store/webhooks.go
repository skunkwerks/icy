package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/skunkwerks/icy/pkg/db"
	"github.com/skunkwerks/icy/pkg/db/models"
)

// WebhookStore is an interface for managing webhooks.
type WebhookStore interface {
	// GetWebhookByID returns a webhook by its ID.
	GetWebhookByID(ctx context.Context, h db.Handler, repoID int64, id int64) (models.Webhook, error)
	// GetWebhooksByRepoID returns all webhooks for a repository.
	GetWebhooksByRepoID(ctx context.Context, h db.Handler, repoID int64) ([]models.Webhook, error)
	// GetWebhooksByRepoIDWhereEvent returns all webhooks for a repository where event is in the events.
	GetWebhooksByRepoIDWhereEvent(ctx context.Context, h db.Handler, repoID int64, events []int) ([]models.Webhook, error)
	// CreateWebhook creates a webhook.
	CreateWebhook(ctx context.Context, h db.Handler, repoID int64, url string, secret string, contentType int, active bool) (int64, error)
	// UpdateWebhookByID updates a webhook by its ID.
	UpdateWebhookByID(ctx context.Context, h db.Handler, repoID int64, id int64, url string, secret string, contentType int, active bool) error
	// DeleteWebhookByID deletes a webhook by its ID.
	DeleteWebhookByID(ctx context.Context, h db.Handler, id int64) error
	// DeleteWebhookForRepoByID deletes a webhook for a repository by its ID.
	DeleteWebhookForRepoByID(ctx context.Context, h db.Handler, repoID int64, id int64) error

	// GetWebhookEventByID returns a webhook event by its ID.
	GetWebhookEventByID(ctx context.Context, h db.Handler, id int64) (models.WebhookEvent, error)
	// GetWebhookEventsByWebhookID returns all webhook events for a webhook.
	GetWebhookEventsByWebhookID(ctx context.Context, h db.Handler, webhookID int64) ([]models.WebhookEvent, error)
	// CreateWebhookEvents creates webhook events for a webhook.
	CreateWebhookEvents(ctx context.Context, h db.Handler, webhookID int64, events []int) error
	// DeleteWebhookEventsByWebhookID deletes all webhook events for a webhook.
	DeleteWebhookEventsByID(ctx context.Context, h db.Handler, ids []int64) error

	// GetWebhookDeliveryByID returns a webhook delivery by its ID.
	GetWebhookDeliveryByID(ctx context.Context, h db.Handler, webhookID int64, id uuid.UUID) (models.WebhookDelivery, error)
	// GetWebhookDeliveriesByWebhookID returns all webhook deliveries for a webhook.
	GetWebhookDeliveriesByWebhookID(ctx context.Context, h db.Handler, webhookID int64) ([]models.WebhookDelivery, error)
	// ListWebhookDeliveriesByWebhookID returns all webhook deliveries for a webhook.
	// This only returns the delivery ID, response status, and event.
	ListWebhookDeliveriesByWebhookID(ctx context.Context, h db.Handler, webhookID int64) ([]models.WebhookDelivery, error)
	// CreateWebhookDelivery creates a webhook delivery.
	CreateWebhookDelivery(ctx context.Context, h db.Handler, id uuid.UUID, webhookID int64, event int, url string, method string, requestError error, requestHeaders string, requestBody string, responseStatus int, responseHeaders string, responseBody string) error
	// DeleteWebhookDeliveryByID deletes a webhook delivery by its ID.
	DeleteWebhookDeliveryByID(ctx context.Context, h db.Handler, webhookID int64, id uuid.UUID) error

	// CreateWebhookQueueItem queues a delivery to be sent at nextAttemptAt
	// (Unix seconds).
	CreateWebhookQueueItem(ctx context.Context, h db.Handler, id uuid.UUID, webhookID int64, event int, contentType int, requestBody string, nextAttemptAt int64) error
	// ListDueWebhookQueueItems returns up to limit queued deliveries that
	// are due at now and not claimed, oldest first, with their webhooks.
	ListDueWebhookQueueItems(ctx context.Context, h db.Handler, now int64, limit int) ([]models.QueuedWebhookDelivery, error)
	// ClaimWebhookQueueItem claims a due delivery until lockUntil and counts
	// one more attempt. attempts must be the count the caller last read. It
	// reports false when the delivery is gone, not due, or someone else
	// claimed it first.
	ClaimWebhookQueueItem(ctx context.Context, h db.Handler, id uuid.UUID, attempts int, now int64, lockUntil int64) (bool, error)
	// RescheduleWebhookQueueItem drops the claim on a delivery and makes it
	// due again at nextAttemptAt.
	RescheduleWebhookQueueItem(ctx context.Context, h db.Handler, id uuid.UUID, nextAttemptAt int64) error
	// ReleaseWebhookQueueItem drops the claim on a delivery without counting
	// the attempt, so it is due again at once.
	ReleaseWebhookQueueItem(ctx context.Context, h db.Handler, id uuid.UUID) error
	// DeleteWebhookQueueItem removes a delivery from the queue.
	DeleteWebhookQueueItem(ctx context.Context, h db.Handler, id uuid.UUID) error
	// ListWebhookQueueItemsByWebhookID returns the queued deliveries of a
	// webhook.
	ListWebhookQueueItemsByWebhookID(ctx context.Context, h db.Handler, webhookID int64) ([]models.WebhookQueueItem, error)
}
