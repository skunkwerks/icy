package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/log/v2"
	"github.com/google/uuid"
	"github.com/skunkwerks/icy/pkg/config"
	"github.com/skunkwerks/icy/pkg/db"
	"github.com/skunkwerks/icy/pkg/db/models"
	"github.com/skunkwerks/icy/pkg/secrets"
	"github.com/skunkwerks/icy/pkg/ssrf"
	"github.com/skunkwerks/icy/pkg/store"
	"github.com/skunkwerks/icy/pkg/version"
)

const (
	// defaultPollInterval is how often the worker looks for due deliveries
	// written by other processes, such as the git hook.
	defaultPollInterval = time.Second
	// defaultConcurrency is how many deliveries are sent at once.
	defaultConcurrency = 4
	// defaultLease is how long a worker holds a claimed delivery. It must
	// be longer than the HTTP client timeout. If the server dies while
	// sending, the delivery is tried again once the lease runs out.
	defaultLease = 2 * time.Minute
	// maxBackoff caps the wait between two attempts.
	maxBackoff = 24 * time.Hour
	// maxResponseBody caps how much of a response is kept in the log.
	maxResponseBody = 64 << 10
)

// RetryPolicy says how often and how far apart a delivery is tried.
type RetryPolicy struct {
	// MaxAttempts is how many times a delivery is tried in all.
	MaxAttempts int
	// Backoff is the wait after the first failure. It doubles after each
	// further failure.
	Backoff time.Duration
}

// Delay returns how long to wait after the given attempt (counted from 1)
// has failed.
func (p RetryPolicy) Delay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := p.Backoff
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= maxBackoff || d <= 0 {
			return maxBackoff
		}
	}
	return min(d, maxBackoff)
}

// Worker sends queued webhook deliveries. Run it in the long-running server
// process only.
type Worker struct {
	db     *db.DB
	store  store.Store
	client *http.Client
	logger *log.Logger
	policy RetryPolicy
	// keyPath is the secrets key file that opens bearer tokens. It is read
	// on each attempt that needs it, so a restored or rotated key takes
	// effect without a restart.
	keyPath string

	poll  time.Duration
	lease time.Duration
	now   func() time.Time

	sem chan struct{}
	wg  sync.WaitGroup
}

// NewWorker returns a worker that takes its database, store, logger and
// settings from ctx. Deliveries are sent with an SSRF-safe client that may
// reach only the private networks the config allows.
func NewWorker(ctx context.Context) *Worker {
	cfg := config.FromContext(ctx)
	policy := RetryPolicy{MaxAttempts: 5, Backoff: 2 * time.Minute}
	client := secureHTTPClient
	keyPath := ""
	if cfg != nil {
		keyPath = secrets.KeyPath(cfg.DataPath)
		if cfg.Webhook.MaxAttempts > 0 {
			policy.MaxAttempts = cfg.Webhook.MaxAttempts
		}
		if cfg.Webhook.RetryBackoff > 0 {
			policy.Backoff = cfg.Webhook.RetryBackoff
		}
		if allow := cfg.Webhook.Allowlist(); len(allow) > 0 {
			client = ssrf.NewSecureClientWithAllowlist(allow)
		}
	}

	return &Worker{
		db:      db.FromContext(ctx),
		store:   store.FromContext(ctx),
		client:  client,
		logger:  log.FromContext(ctx).WithPrefix("webhook"),
		policy:  policy,
		keyPath: keyPath,
		poll:    defaultPollInterval,
		lease:   defaultLease,
		now:     time.Now,
		sem:     make(chan struct{}, defaultConcurrency),
	}
}

// Run sends due deliveries until ctx is done, then waits for the ones in
// flight. A delivery cut off by shutdown is released untouched and goes out
// on the next start.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()

	for {
		w.dispatch(ctx)
		select {
		case <-ctx.Done():
			w.wg.Wait()
			return
		case <-ticker.C:
		case <-wake:
		}
	}
}

// ProcessDue sends every delivery that is due now and waits for them to
// finish. It returns how many it sent.
func (w *Worker) ProcessDue(ctx context.Context) int {
	n := 0
	for ctx.Err() == nil {
		started := w.dispatch(ctx)
		w.wg.Wait()
		if started == 0 {
			break
		}
		n += started
	}
	return n
}

// dispatch claims as many due deliveries as there are free senders and
// starts sending them. It returns how many it started.
func (w *Worker) dispatch(ctx context.Context) int {
	free := cap(w.sem) - len(w.sem)
	if free == 0 || ctx.Err() != nil {
		return 0
	}

	now := w.now().Unix()
	items, err := w.store.ListDueWebhookQueueItems(ctx, w.db, now, free)
	if err != nil {
		if ctx.Err() == nil {
			w.logger.Error("listing queued webhook deliveries", "err", err)
		}
		return 0
	}

	started := 0
	for _, item := range items {
		ok, err := w.store.ClaimWebhookQueueItem(ctx, w.db, item.ID, item.Attempts, now, now+int64(w.lease/time.Second))
		if err != nil {
			w.logger.Error("claiming webhook delivery", "delivery", item.ID, "err", err)
			continue
		}
		if !ok {
			// Another worker got there first.
			continue
		}

		w.sem <- struct{}{}
		w.wg.Add(1)
		started++
		go func(item models.QueuedWebhookDelivery) {
			defer func() {
				<-w.sem
				w.wg.Done()
			}()
			w.process(ctx, item, item.Attempts+1)
		}(item)
	}
	return started
}

// process makes one attempt at a claimed delivery, records it, and then
// removes or reschedules the delivery.
func (w *Worker) process(ctx context.Context, item models.QueuedWebhookDelivery, attempt int) {
	logger := w.logger.With("webhook", item.WebhookID, "delivery", item.ID, "attempt", attempt)

	// The bookkeeping below must happen even when shutdown has started.
	bg := context.WithoutCancel(ctx)

	if !item.Active {
		// The webhook was switched off after the event was queued.
		if err := w.store.DeleteWebhookQueueItem(bg, w.db, item.ID); err != nil {
			logger.Error("dropping delivery for inactive webhook", "err", err)
		}
		return
	}

	rec, sendErr := w.send(ctx, item)
	if sendErr != nil && ctx.Err() != nil {
		// Shutdown cut the request off. That says nothing about the
		// receiver, so do not count it.
		if err := w.store.ReleaseWebhookQueueItem(bg, w.db, item.ID); err != nil {
			logger.Error("releasing webhook delivery", "err", err)
		}
		return
	}

	// The first attempt is logged under the delivery ID itself, so the ID
	// a receiver sees can be looked up directly. Retries get their own
	// log entries, and their headers carry the same delivery ID.
	logID := item.ID
	if attempt > 1 {
		logID = uuid.New()
	}
	if err := w.store.CreateWebhookDelivery(bg, w.db, logID, item.WebhookID, item.Event, item.URL, http.MethodPost,
		sendErr, rec.reqHeaders, item.RequestBody, rec.status, rec.resHeaders, rec.resBody); err != nil {
		logger.Error("recording webhook delivery", "err", err)
	}

	success := sendErr == nil && rec.status >= 200 && rec.status < 300
	switch {
	case success:
		logger.Debug("webhook delivered", "status", rec.status)
		if err := w.store.DeleteWebhookQueueItem(bg, w.db, item.ID); err != nil {
			logger.Error("removing delivered webhook from queue", "err", err)
		}
	case sendErr == nil && (rec.status == http.StatusUnauthorized || rec.status == http.StatusForbidden):
		// The receiver refused the credentials. Sending the same ones
		// again will not help, and could get the token locked out.
		logger.Warn("webhook delivery refused, not retrying", "status", rec.status)
		if err := w.store.DeleteWebhookQueueItem(bg, w.db, item.ID); err != nil {
			logger.Error("removing refused webhook from queue", "err", err)
		}
	case attempt >= w.policy.MaxAttempts:
		logger.Warn("webhook delivery failed, giving up", "status", rec.status, "err", sendErr)
		if err := w.store.DeleteWebhookQueueItem(bg, w.db, item.ID); err != nil {
			logger.Error("removing failed webhook from queue", "err", err)
		}
	default:
		next := w.now().Add(w.policy.Delay(attempt))
		logger.Info("webhook delivery failed, will retry", "status", rec.status, "err", sendErr, "next", next.Format(time.RFC3339))
		if err := w.store.RescheduleWebhookQueueItem(bg, w.db, item.ID, next.Unix()); err != nil {
			logger.Error("rescheduling webhook delivery", "err", err)
		}
	}
}

// record is what is kept of one attempt.
type record struct {
	reqHeaders string
	status     int
	resHeaders string
	resBody    string
}

// send posts a delivery once.
func (w *Worker) send(ctx context.Context, item models.QueuedWebhookDelivery) (record, error) {
	contentType := ContentType(item.ContentType) //nolint:gosec

	headers := http.Header{}
	headers.Add("Content-Type", contentType.String())
	headers.Add("User-Agent", "Icy/"+version.Version)
	headers.Add("X-Icy-Event", Event(item.Event).String())
	headers.Add("X-Icy-Delivery", item.ID.String())
	if item.Secret != "" {
		sig := hmac.New(sha256.New, []byte(item.Secret))
		sig.Write([]byte(item.RequestBody)) //nolint:errcheck
		headers.Add("X-Icy-Signature", "sha256="+hex.EncodeToString(sig.Sum(nil)))
	}

	if item.BearerToken != "" {
		token, err := w.bearerToken(item.BearerToken)
		if err != nil {
			// Never send without the token. The detail goes to the
			// server log only, since repo admins read the delivery log.
			w.logger.Error("cannot open webhook bearer token", "webhook", item.WebhookID, "delivery", item.ID, "err", err)
			return record{reqHeaders: formatHeaders(headers)}, errBearerUnavailable
		}
		headers.Add("Authorization", "Bearer "+token)
	}

	rec := record{reqHeaders: formatHeaders(headers)}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, item.URL, bytes.NewReader([]byte(item.RequestBody)))
	if err != nil {
		return rec, err //nolint:wrapcheck
	}
	req.Header = headers

	res, err := w.client.Do(req)
	if err != nil {
		return rec, err //nolint:wrapcheck
	}
	defer res.Body.Close() //nolint:errcheck

	rec.status = res.StatusCode
	rec.resHeaders = formatHeaders(res.Header)
	b, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBody))
	rec.resBody = string(b)
	if err != nil && !errors.Is(err, io.EOF) {
		return rec, err //nolint:wrapcheck
	}
	return rec, nil
}

// formatHeaders writes headers one per line, in a stable order.
func formatHeaders(h http.Header) string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		v := h.Get(k)
		if k == "Authorization" {
			v = redact(v)
		}
		b.WriteString(k + ": " + v + "\n")
	}
	return b.String()
}

// redact hides the credentials of an Authorization header value and keeps
// its scheme, such as Bearer.
func redact(v string) string {
	scheme, _, _ := strings.Cut(v, " ")
	return scheme + " [REDACTED]"
}

// errBearerUnavailable is recorded when a webhook has a bearer token that
// cannot be opened. Nothing is sent.
var errBearerUnavailable = errors.New("bearer token cannot be decrypted, see the server log")

// bearerToken opens a sealed bearer token with the secrets key.
func (w *Worker) bearerToken(sealed string) (string, error) {
	if w.keyPath == "" {
		return "", secrets.ErrNoKey
	}
	key, err := secrets.LoadKey(w.keyPath)
	if err != nil {
		return "", err //nolint:wrapcheck
	}
	token, err := secrets.Decrypt(key, sealed)
	if err != nil {
		return "", err //nolint:wrapcheck
	}
	return string(token), nil
}
