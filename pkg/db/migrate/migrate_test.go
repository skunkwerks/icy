package migrate

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/skunkwerks/icy/pkg/config"
	"github.com/skunkwerks/icy/pkg/db"
	"github.com/skunkwerks/icy/pkg/db/internal/test"
)

func TestMigrate(t *testing.T) {
	// XXX: we need a config.Config in the context for the migrations to run
	// properly. Some migrations depend on the config being present.
	ctx := config.WithContext(context.TODO(), config.DefaultConfig())
	dbx, err := test.OpenSqlite(ctx, t)
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, dbx); err != nil {
		t.Errorf("Migrate() => %v, want nil error", err)
	}
}

// openTestDB opens an empty database: a scratch postgres database when
// ICY_TEST_POSTGRES_DSN names a server, sqlite otherwise.
func openTestDB(ctx context.Context, t *testing.T) *db.DB {
	t.Helper()
	dsn := os.Getenv("ICY_TEST_POSTGRES_DSN")
	if dsn == "" {
		dbx, err := test.OpenSqlite(ctx, t)
		if err != nil {
			t.Fatal(err)
		}
		return dbx
	}

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("icy_migrate_test_%d", time.Now().UnixNano())
	admin, err := db.Open(ctx, "postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	dbx, err := db.Open(ctx, "postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		dbx.Close()                                                             //nolint:errcheck
		admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)") //nolint:errcheck
		admin.Close()                                                           //nolint:errcheck
	})
	return dbx
}

func hasColumn(t *testing.T, dbx *db.DB, table, column string) bool {
	t.Helper()
	rows, err := dbx.Queryx("SELECT * FROM " + table + " LIMIT 0")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close() //nolint:errcheck
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	return slices.Contains(cols, column)
}

func TestWebhookBearerMigration(t *testing.T) {
	ctx := config.WithContext(context.TODO(), config.DefaultConfig())
	dbx := openTestDB(ctx, t)
	if err := Migrate(ctx, dbx); err != nil {
		t.Fatal(err)
	}

	// A webhook made without the new columns gets their defaults.
	if _, err := dbx.ExecContext(ctx, `INSERT INTO repos (name, project_name, description, private, mirror, hidden, user_id, updated_at)
		SELECT 'repo', '', '', false, false, false, id, CURRENT_TIMESTAMP FROM users WHERE username = 'admin'`); err != nil {
		t.Fatal(err)
	}
	if _, err := dbx.ExecContext(ctx, `INSERT INTO webhooks (repo_id, url, secret, content_type, active, updated_at)
		SELECT id, 'https://example.com/', '', 1, true, CURRENT_TIMESTAMP FROM repos`); err != nil {
		t.Fatal(err)
	}
	var row struct {
		BearerToken string `db:"bearer_token"`
		Format      string `db:"format"`
	}
	if err := dbx.GetContext(ctx, &row, "SELECT bearer_token, format FROM webhooks"); err != nil {
		t.Fatal(err)
	}
	if row.BearerToken != "" || row.Format != "icy" {
		t.Errorf("new webhook columns = %+v, want empty token and format icy", row)
	}

	// Rolling back drops both columns and keeps the webhook.
	if err := Rollback(ctx, dbx); err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"bearer_token", "format"} {
		if hasColumn(t, dbx, "webhooks", col) {
			t.Errorf("column %s still there after rollback", col)
		}
	}
	var n int
	if err := dbx.GetContext(ctx, &n, "SELECT COUNT(*) FROM webhooks"); err != nil || n != 1 {
		t.Errorf("webhooks after rollback = %d, %v, want 1", n, err)
	}

	// And it can be applied again.
	if err := Migrate(ctx, dbx); err != nil {
		t.Fatal(err)
	}
	if !hasColumn(t, dbx, "webhooks", "bearer_token") || !hasColumn(t, dbx, "webhooks", "format") {
		t.Error("columns missing after migrating again")
	}
}
