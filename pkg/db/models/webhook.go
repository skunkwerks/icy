package models

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// Webhook is a repository webhook.
type Webhook struct {
	ID          int64     `db:"id"`
	RepoID      int64     `db:"repo_id"`
	URL         string    `db:"url"`
	Secret      string    `db:"secret"`
	ContentType int       `db:"content_type"`
	Active      bool      `db:"active"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

// WebhookEvent is a webhook event.
type WebhookEvent struct {
	ID        int64     `db:"id"`
	WebhookID int64     `db:"webhook_id"`
	Event     int       `db:"event"`
	CreatedAt time.Time `db:"created_at"`
}

// WebhookDelivery is a webhook delivery.
type WebhookDelivery struct {
	ID              uuid.UUID      `db:"id"`
	WebhookID       int64          `db:"webhook_id"`
	Event           int            `db:"event"`
	RequestURL      string         `db:"request_url"`
	RequestMethod   string         `db:"request_method"`
	RequestError    sql.NullString `db:"request_error"`
	RequestHeaders  string         `db:"request_headers"`
	RequestBody     string         `db:"request_body"`
	ResponseStatus  int            `db:"response_status"`
	ResponseHeaders string         `db:"response_headers"`
	ResponseBody    string         `db:"response_body"`
	CreatedAt       time.Time      `db:"created_at"`
}

// WebhookQueueItem is a webhook delivery waiting to be sent. Its ID is the
// delivery ID sent in the X-Icy-Delivery header, and stays the same across
// retries.
type WebhookQueueItem struct {
	ID          uuid.UUID `db:"id"`
	WebhookID   int64     `db:"webhook_id"`
	Event       int       `db:"event"`
	ContentType int       `db:"content_type"`
	RequestBody string    `db:"request_body"`
	// Attempts is how many times sending has been started.
	Attempts int `db:"attempts"`
	// NextAttemptAt is when the delivery is next due, in Unix seconds.
	NextAttemptAt int64 `db:"next_attempt_at"`
	// LockedUntil is when a worker's claim on the delivery runs out, in
	// Unix seconds.
	LockedUntil int64     `db:"locked_until"`
	CreatedAt   time.Time `db:"created_at"`
}

// QueuedWebhookDelivery is a due queue item with the webhook it goes to.
type QueuedWebhookDelivery struct {
	WebhookQueueItem
	URL    string `db:"url"`
	Secret string `db:"secret"`
	Active bool   `db:"active"`
}
