CREATE TABLE IF NOT EXISTS webhook_queue (
  id TEXT PRIMARY KEY,
  webhook_id INTEGER NOT NULL,
  event INTEGER NOT NULL,
  content_type INTEGER NOT NULL,
  request_body TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  next_attempt_at BIGINT NOT NULL,
  locked_until BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT webhook_id_fk
  FOREIGN KEY(webhook_id) REFERENCES webhooks(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE
);

CREATE INDEX IF NOT EXISTS webhook_queue_next_attempt_at_idx ON webhook_queue (next_attempt_at);
CREATE INDEX IF NOT EXISTS webhook_queue_webhook_id_idx ON webhook_queue (webhook_id);
