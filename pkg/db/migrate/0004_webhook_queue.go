package migrate

import (
	"context"

	"github.com/skunkwerks/icy/pkg/db"
)

const (
	webhookQueueName    = "webhook_queue"
	webhookQueueVersion = 4
)

// webhookQueue adds the table that holds webhook deliveries waiting to be
// sent. The git hook process writes rows and the server sends them.
var webhookQueue = Migration{
	Name:    webhookQueueName,
	Version: webhookQueueVersion,
	Migrate: func(ctx context.Context, tx *db.Tx) error {
		return migrateUp(ctx, tx, webhookQueueVersion, webhookQueueName)
	},
	Rollback: func(ctx context.Context, tx *db.Tx) error {
		return migrateDown(ctx, tx, webhookQueueVersion, webhookQueueName)
	},
}
