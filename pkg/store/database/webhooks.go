package database

import (
	"context"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/skunkwerks/icy/pkg/db"
	"github.com/skunkwerks/icy/pkg/db/models"
	"github.com/skunkwerks/icy/pkg/store"
)

type webhookStore struct{}

var _ store.WebhookStore = (*webhookStore)(nil)

// CreateWebhook implements store.WebhookStore.
func (*webhookStore) CreateWebhook(ctx context.Context, h db.Handler, repoID int64, url string, secret string, contentType int, active bool) (int64, error) {
	var id int64
	query := h.Rebind(`INSERT INTO webhooks (repo_id, url, secret, content_type, active, updated_at)
			VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP) RETURNING id;`)
	err := h.GetContext(ctx, &id, query, repoID, url, secret, contentType, active)
	if err != nil {
		return 0, err
	}

	return id, nil
}

// CreateWebhookDelivery implements store.WebhookStore.
func (*webhookStore) CreateWebhookDelivery(ctx context.Context, h db.Handler, id uuid.UUID, webhookID int64, event int, url string, method string, requestError error, requestHeaders string, requestBody string, responseStatus int, responseHeaders string, responseBody string) error {
	query := h.Rebind(`INSERT INTO webhook_deliveries (id, webhook_id, event, request_url, request_method, request_error, request_headers, request_body, response_status, response_headers, response_body)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`)
	var reqErr string
	if requestError != nil {
		reqErr = requestError.Error()
	}
	_, err := h.ExecContext(ctx, query, id, webhookID, event, url, method, reqErr, requestHeaders, requestBody, responseStatus, responseHeaders, responseBody)
	return err
}

// CreateWebhookEvents implements store.WebhookStore.
func (*webhookStore) CreateWebhookEvents(ctx context.Context, h db.Handler, webhookID int64, events []int) error {
	query := h.Rebind(`INSERT INTO webhook_events (webhook_id, event)
			VALUES (?, ?);`)
	for _, event := range events {
		_, err := h.ExecContext(ctx, query, webhookID, event)
		if err != nil {
			return err
		}
	}
	return nil
}

// DeleteWebhookByID implements store.WebhookStore.
func (*webhookStore) DeleteWebhookByID(ctx context.Context, h db.Handler, id int64) error {
	query := h.Rebind(`DELETE FROM webhooks WHERE id = ?;`)
	_, err := h.ExecContext(ctx, query, id)
	return err
}

// DeleteWebhookForRepoByID implements store.WebhookStore.
func (*webhookStore) DeleteWebhookForRepoByID(ctx context.Context, h db.Handler, repoID int64, id int64) error {
	query := h.Rebind(`DELETE FROM webhooks WHERE repo_id = ? AND id = ?;`)
	_, err := h.ExecContext(ctx, query, repoID, id)
	return err
}

// DeleteWebhookDeliveryByID implements store.WebhookStore.
func (*webhookStore) DeleteWebhookDeliveryByID(ctx context.Context, h db.Handler, webhookID int64, id uuid.UUID) error {
	query := h.Rebind(`DELETE FROM webhook_deliveries WHERE webhook_id = ? AND id = ?;`)
	_, err := h.ExecContext(ctx, query, webhookID, id)
	return err
}

// DeleteWebhookEventsByWebhookID implements store.WebhookStore.
func (*webhookStore) DeleteWebhookEventsByID(ctx context.Context, h db.Handler, ids []int64) error {
	// sqlx.In refuses an empty list, and an update that keeps every event
	// has nothing to delete.
	if len(ids) == 0 {
		return nil
	}
	query, args, err := sqlx.In(`DELETE FROM webhook_events WHERE id IN (?);`, ids)
	if err != nil {
		return err
	}

	query = h.Rebind(query)
	_, err = h.ExecContext(ctx, query, args...)
	return err
}

// GetWebhookByID implements store.WebhookStore.
func (*webhookStore) GetWebhookByID(ctx context.Context, h db.Handler, repoID int64, id int64) (models.Webhook, error) {
	query := h.Rebind(`SELECT * FROM webhooks WHERE repo_id = ? AND id = ?;`)
	var wh models.Webhook
	err := h.GetContext(ctx, &wh, query, repoID, id)
	return wh, err
}

// GetWebhookDeliveriesByWebhookID implements store.WebhookStore.
func (*webhookStore) GetWebhookDeliveriesByWebhookID(ctx context.Context, h db.Handler, webhookID int64) ([]models.WebhookDelivery, error) {
	query := h.Rebind(`SELECT * FROM webhook_deliveries WHERE webhook_id = ?;`)
	var whds []models.WebhookDelivery
	err := h.SelectContext(ctx, &whds, query, webhookID)
	return whds, err
}

// GetWebhookDeliveryByID implements store.WebhookStore.
func (*webhookStore) GetWebhookDeliveryByID(ctx context.Context, h db.Handler, webhookID int64, id uuid.UUID) (models.WebhookDelivery, error) {
	query := h.Rebind(`SELECT * FROM webhook_deliveries WHERE webhook_id = ? AND id = ?;`)
	var whd models.WebhookDelivery
	err := h.GetContext(ctx, &whd, query, webhookID, id)
	return whd, err
}

// GetWebhookEventByID implements store.WebhookStore.
func (*webhookStore) GetWebhookEventByID(ctx context.Context, h db.Handler, id int64) (models.WebhookEvent, error) {
	query := h.Rebind(`SELECT * FROM webhook_events WHERE id = ?;`)
	var whe models.WebhookEvent
	err := h.GetContext(ctx, &whe, query, id)
	return whe, err
}

// GetWebhookEventsByWebhookID implements store.WebhookStore.
func (*webhookStore) GetWebhookEventsByWebhookID(ctx context.Context, h db.Handler, webhookID int64) ([]models.WebhookEvent, error) {
	query := h.Rebind(`SELECT * FROM webhook_events WHERE webhook_id = ?;`)
	var whes []models.WebhookEvent
	err := h.SelectContext(ctx, &whes, query, webhookID)
	return whes, err
}

// GetWebhooksByRepoID implements store.WebhookStore.
func (*webhookStore) GetWebhooksByRepoID(ctx context.Context, h db.Handler, repoID int64) ([]models.Webhook, error) {
	query := h.Rebind(`SELECT * FROM webhooks WHERE repo_id = ?;`)
	var whs []models.Webhook
	err := h.SelectContext(ctx, &whs, query, repoID)
	return whs, err
}

// GetWebhooksByRepoIDWhereEvent implements store.WebhookStore.
func (*webhookStore) GetWebhooksByRepoIDWhereEvent(ctx context.Context, h db.Handler, repoID int64, events []int) ([]models.Webhook, error) {
	query, args, err := sqlx.In(`SELECT webhooks.*
			FROM webhooks
			INNER JOIN webhook_events ON webhooks.id = webhook_events.webhook_id
			WHERE webhooks.repo_id = ? AND webhook_events.event IN (?);`, repoID, events)
	if err != nil {
		return nil, err
	}

	query = h.Rebind(query)
	var whs []models.Webhook
	err = h.SelectContext(ctx, &whs, query, args...)
	return whs, err
}

// ListWebhookDeliveriesByWebhookID implements store.WebhookStore.
func (*webhookStore) ListWebhookDeliveriesByWebhookID(ctx context.Context, h db.Handler, webhookID int64) ([]models.WebhookDelivery, error) {
	query := h.Rebind(`SELECT id, response_status, event, created_at FROM webhook_deliveries WHERE webhook_id = ? ORDER BY created_at;`)
	var whds []models.WebhookDelivery
	err := h.SelectContext(ctx, &whds, query, webhookID)
	return whds, err
}

// UpdateWebhookByID implements store.WebhookStore.
func (*webhookStore) UpdateWebhookByID(ctx context.Context, h db.Handler, repoID int64, id int64, url string, secret string, contentType int, active bool) error {
	query := h.Rebind(`UPDATE webhooks SET url = ?, secret = ?, content_type = ?, active = ?, updated_at = CURRENT_TIMESTAMP WHERE repo_id = ? AND id = ?;`)
	_, err := h.ExecContext(ctx, query, url, secret, contentType, active, repoID, id)
	return err
}

// CreateWebhookQueueItem implements store.WebhookStore.
func (*webhookStore) CreateWebhookQueueItem(ctx context.Context, h db.Handler, id uuid.UUID, webhookID int64, event int, contentType int, requestBody string, nextAttemptAt int64) error {
	query := h.Rebind(`INSERT INTO webhook_queue (id, webhook_id, event, content_type, request_body, next_attempt_at)
			VALUES (?, ?, ?, ?, ?, ?);`)
	_, err := h.ExecContext(ctx, query, id, webhookID, event, contentType, requestBody, nextAttemptAt)
	return err
}

// ListDueWebhookQueueItems implements store.WebhookStore.
func (*webhookStore) ListDueWebhookQueueItems(ctx context.Context, h db.Handler, now int64, limit int) ([]models.QueuedWebhookDelivery, error) {
	query := h.Rebind(`SELECT webhook_queue.*, webhooks.url, webhooks.secret, webhooks.active
			FROM webhook_queue
			INNER JOIN webhooks ON webhooks.id = webhook_queue.webhook_id
			WHERE webhook_queue.next_attempt_at <= ? AND webhook_queue.locked_until <= ?
			ORDER BY webhook_queue.next_attempt_at, webhook_queue.created_at
			LIMIT ?;`)
	var items []models.QueuedWebhookDelivery
	err := h.SelectContext(ctx, &items, query, now, now, limit)
	return items, err
}

// ClaimWebhookQueueItem implements store.WebhookStore.
func (*webhookStore) ClaimWebhookQueueItem(ctx context.Context, h db.Handler, id uuid.UUID, attempts int, now int64, lockUntil int64) (bool, error) {
	// The WHERE clause repeats every condition the caller saw, so of two
	// workers racing for the same row only one update matches.
	query := h.Rebind(`UPDATE webhook_queue SET attempts = attempts + 1, locked_until = ?
			WHERE id = ? AND attempts = ? AND next_attempt_at <= ? AND locked_until <= ?;`)
	res, err := h.ExecContext(ctx, query, lockUntil, id, attempts, now, now)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// RescheduleWebhookQueueItem implements store.WebhookStore.
func (*webhookStore) RescheduleWebhookQueueItem(ctx context.Context, h db.Handler, id uuid.UUID, nextAttemptAt int64) error {
	query := h.Rebind(`UPDATE webhook_queue SET next_attempt_at = ?, locked_until = 0 WHERE id = ?;`)
	_, err := h.ExecContext(ctx, query, nextAttemptAt, id)
	return err
}

// ReleaseWebhookQueueItem implements store.WebhookStore.
func (*webhookStore) ReleaseWebhookQueueItem(ctx context.Context, h db.Handler, id uuid.UUID) error {
	query := h.Rebind(`UPDATE webhook_queue SET attempts = attempts - 1, locked_until = 0 WHERE id = ? AND attempts > 0;`)
	_, err := h.ExecContext(ctx, query, id)
	return err
}

// DeleteWebhookQueueItem implements store.WebhookStore.
func (*webhookStore) DeleteWebhookQueueItem(ctx context.Context, h db.Handler, id uuid.UUID) error {
	query := h.Rebind(`DELETE FROM webhook_queue WHERE id = ?;`)
	_, err := h.ExecContext(ctx, query, id)
	return err
}

// ListWebhookQueueItemsByWebhookID implements store.WebhookStore.
func (*webhookStore) ListWebhookQueueItemsByWebhookID(ctx context.Context, h db.Handler, webhookID int64) ([]models.WebhookQueueItem, error) {
	query := h.Rebind(`SELECT * FROM webhook_queue WHERE webhook_id = ? ORDER BY created_at;`)
	var items []models.WebhookQueueItem
	err := h.SelectContext(ctx, &items, query, webhookID)
	return items, err
}
