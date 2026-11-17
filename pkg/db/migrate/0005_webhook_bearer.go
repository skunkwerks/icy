package migrate

import (
	"context"

	"github.com/skunkwerks/icy/pkg/db"
)

const (
	webhookBearerName    = "webhook_bearer"
	webhookBearerVersion = 5
)

// webhookBearer adds an encrypted bearer token and a payload format to
// webhooks. The token is sealed with the key in the data directory, so the
// column alone does not reveal it.
var webhookBearer = Migration{
	Name:    webhookBearerName,
	Version: webhookBearerVersion,
	Migrate: func(ctx context.Context, tx *db.Tx) error {
		return migrateUp(ctx, tx, webhookBearerVersion, webhookBearerName)
	},
	Rollback: func(ctx context.Context, tx *db.Tx) error {
		return migrateDown(ctx, tx, webhookBearerVersion, webhookBearerName)
	},
}
