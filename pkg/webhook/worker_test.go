package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/log/v2"
	"github.com/google/uuid"
	"github.com/skunkwerks/icy/pkg/config"
	"github.com/skunkwerks/icy/pkg/db"
	"github.com/skunkwerks/icy/pkg/db/migrate"
	"github.com/skunkwerks/icy/pkg/db/models"
	"github.com/skunkwerks/icy/pkg/store"
	"github.com/skunkwerks/icy/pkg/store/database"
	_ "modernc.org/sqlite"
)

// queueEnv is a database with one repository, set up the way the server
// sets up its context. Webhooks may reach 127.0.0.1, where the httptest
// receivers listen.
type queueEnv struct {
	t      *testing.T
	ctx    context.Context
	path   string
	cfg    *config.Config
	db     *db.DB
	store  store.Store
	repoID int64
	clock  atomic.Int64
}

func newQueueEnv(t *testing.T) *queueEnv {
	t.Helper()
	dp := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.DataPath = dp
	cfg.DB.Driver = "sqlite"
	cfg.DB.DataSource = filepath.Join(dp, "test.db") + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	cfg.Webhook.AllowedNetworks = []string{"127.0.0.1"}
	cfg.Webhook.MaxAttempts = 3
	cfg.Webhook.RetryBackoff = time.Minute
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if dsn := os.Getenv("ICY_TEST_POSTGRES_DSN"); dsn != "" {
		cfg.DB.Driver = "postgres"
		cfg.DB.DataSource = newPostgresDB(t, dsn)
	}

	e := &queueEnv{t: t, path: cfg.DB.DataSource, cfg: cfg}
	// The worker's clock runs ahead of the wall clock that SendEvent uses to
	// queue events, so whatever a test queues is due at once.
	e.clock.Store(time.Now().Add(time.Hour).Unix())
	e.open()

	if err := migrate.Migrate(e.ctx, e.db); err != nil {
		t.Fatal(err)
	}
	if err := e.store.CreateRepo(e.ctx, e.db, "repo", 1, "", "", false, false, false); err != nil {
		t.Fatal(err)
	}
	r, err := e.store.GetRepoByName(e.ctx, e.db, "repo")
	if err != nil {
		t.Fatal(err)
	}
	e.repoID = r.ID
	return e
}

// newPostgresDB creates a scratch database on the server named by dsn, such
// as postgres://postgres@localhost:5432/postgres?sslmode=disable, and
// returns a DSN for it. It is dropped when the test ends.
func newPostgresDB(t *testing.T, dsn string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("icy_webhook_test_%d", time.Now().UnixNano())
	admin, err := db.Open(context.Background(), "postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(context.Background(), "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)") //nolint:errcheck
		admin.Close()                                                                            //nolint:errcheck
	})
	u.Path = "/" + name
	return u.String()
}

// open (re)opens the database, as a new server process would.
func (e *queueEnv) open() {
	ctx := config.WithContext(context.Background(), e.cfg)
	ctx = log.WithContext(ctx, log.New(io.Discard))
	dbx, err := db.Open(ctx, e.cfg.DB.Driver, e.path)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { dbx.Close() }) //nolint:errcheck
	st := database.New(ctx, dbx)
	e.db = dbx
	e.store = st
	e.ctx = store.WithContext(db.WithContext(ctx, dbx), st)
}

func (e *queueEnv) worker() *Worker {
	w := NewWorker(e.ctx)
	w.now = func() time.Time { return time.Unix(e.clock.Load(), 0) }
	return w
}

func (e *queueEnv) advance(d time.Duration) {
	e.clock.Add(int64(d / time.Second))
}

func (e *queueEnv) addWebhook(url, secret string, events ...Event) int64 {
	e.t.Helper()
	id, err := e.store.CreateWebhook(e.ctx, e.db, e.repoID, url, secret, int(ContentTypeJSON), true)
	if err != nil {
		e.t.Fatal(err)
	}
	evs := make([]int, len(events))
	for i, ev := range events {
		evs[i] = int(ev)
	}
	if err := e.store.CreateWebhookEvents(e.ctx, e.db, id, evs); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *queueEnv) push() {
	e.t.Helper()
	payload := Common{EventType: EventPush, Repository: Repository{ID: e.repoID, Name: "repo"}}
	if err := SendEvent(e.ctx, payload); err != nil {
		e.t.Fatal(err)
	}
}

func (e *queueEnv) queued(webhookID int64) []models.WebhookQueueItem {
	e.t.Helper()
	items, err := e.store.ListWebhookQueueItemsByWebhookID(e.ctx, e.db, webhookID)
	if err != nil {
		e.t.Fatal(err)
	}
	return items
}

func (e *queueEnv) delivered(webhookID int64) []models.WebhookDelivery {
	e.t.Helper()
	ds, err := e.store.GetWebhookDeliveriesByWebhookID(e.ctx, e.db, webhookID)
	if err != nil {
		e.t.Fatal(err)
	}
	return ds
}

// receiver is an httptest server that records what it is sent and answers
// with the status codes it is given, then 200.
type receiver struct {
	*httptest.Server
	mu       sync.Mutex
	statuses []int
	requests []*http.Request
	bodies   []string
}

func newReceiver(t *testing.T, statuses ...int) *receiver {
	r := &receiver{statuses: statuses}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.requests = append(r.requests, req)
		r.bodies = append(r.bodies, string(body))
		status := http.StatusOK
		if len(r.statuses) > 0 {
			status, r.statuses = r.statuses[0], r.statuses[1:]
		}
		r.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *receiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func TestRetryPolicyDelay(t *testing.T) {
	p := RetryPolicy{MaxAttempts: 5, Backoff: 2 * time.Minute}
	want := []time.Duration{2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute}
	var total time.Duration
	for i, w := range want {
		if got := p.Delay(i + 1); got != w {
			t.Errorf("Delay(%d) = %s, want %s", i+1, got, w)
		}
		total += p.Delay(i + 1)
	}
	if total != 30*time.Minute {
		t.Errorf("default retries span %s, want 30m", total)
	}
	if got := p.Delay(100); got != maxBackoff {
		t.Errorf("Delay(100) = %s, want the %s cap", got, maxBackoff)
	}
	if got := p.Delay(0); got != 2*time.Minute {
		t.Errorf("Delay(0) = %s, want 2m", got)
	}
}

func TestQueueDeliversWithHeadersAndSignature(t *testing.T) {
	e := newQueueEnv(t)
	rcv := newReceiver(t)
	hook := e.addWebhook(rcv.URL+"/hook", "s3cret", EventPush)

	e.push()
	queued := e.queued(hook)
	if len(queued) != 1 {
		t.Fatalf("queued %d deliveries, want 1", len(queued))
	}
	if rcv.count() != 0 {
		t.Fatal("SendEvent sent the webhook itself")
	}

	if n := e.worker().ProcessDue(e.ctx); n != 1 {
		t.Fatalf("ProcessDue sent %d, want 1", n)
	}
	if rcv.count() != 1 {
		t.Fatalf("receiver got %d requests, want 1", rcv.count())
	}

	req, body := rcv.requests[0], rcv.bodies[0]
	if got := req.Header.Get("X-Icy-Event"); got != "push" {
		t.Errorf("X-Icy-Event = %q, want push", got)
	}
	if got := req.Header.Get("X-Icy-Delivery"); got != queued[0].ID.String() {
		t.Errorf("X-Icy-Delivery = %q, want %s", got, queued[0].ID)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write([]byte(body))
	if got, want := req.Header.Get("X-Icy-Signature"), "sha256="+hex.EncodeToString(mac.Sum(nil)); got != want {
		t.Errorf("X-Icy-Signature = %q, want %q", got, want)
	}

	if len(e.queued(hook)) != 0 {
		t.Error("delivered webhook still queued")
	}
	ds := e.delivered(hook)
	if len(ds) != 1 {
		t.Fatalf("recorded %d deliveries, want 1", len(ds))
	}
	if ds[0].ID != queued[0].ID || ds[0].ResponseStatus != 200 || ds[0].RequestBody != body {
		t.Errorf("recorded delivery = %+v", ds[0])
	}
}

func TestQueueRetriesWithBackoff(t *testing.T) {
	e := newQueueEnv(t)
	rcv := newReceiver(t, 500, 503)
	hook := e.addWebhook(rcv.URL, "", EventPush)
	w := e.worker()

	e.push()
	id := e.queued(hook)[0].ID

	// First attempt fails and is put back one backoff later.
	start := e.clock.Load()
	if n := w.ProcessDue(e.ctx); n != 1 {
		t.Fatalf("first pass sent %d, want 1", n)
	}
	q := e.queued(hook)
	if len(q) != 1 || q[0].Attempts != 1 || q[0].LockedUntil != 0 {
		t.Fatalf("after one failure queue = %+v", q)
	}
	if got := q[0].NextAttemptAt - start; got != 60 {
		t.Errorf("first retry in %ds, want 60", got)
	}

	// Nothing is due until the backoff has passed.
	e.advance(59 * time.Second)
	if n := w.ProcessDue(e.ctx); n != 0 {
		t.Fatalf("sent %d before the backoff ran out", n)
	}

	// Second attempt fails, and the wait doubles.
	e.advance(time.Second)
	if n := w.ProcessDue(e.ctx); n != 1 {
		t.Fatalf("second pass sent %d, want 1", n)
	}
	q = e.queued(hook)
	if len(q) != 1 || q[0].Attempts != 2 || q[0].NextAttemptAt-e.clock.Load() != 120 {
		t.Fatalf("after two failures queue = %+v", q)
	}

	// Third attempt succeeds.
	e.advance(2 * time.Minute)
	if n := w.ProcessDue(e.ctx); n != 1 {
		t.Fatalf("third pass sent %d, want 1", n)
	}
	if len(e.queued(hook)) != 0 {
		t.Error("delivered webhook still queued")
	}

	// Every attempt is in the log, and all carry the same delivery ID.
	ds := e.delivered(hook)
	if len(ds) != 3 {
		t.Fatalf("recorded %d attempts, want 3", len(ds))
	}
	for i, r := range rcv.requests {
		if got := r.Header.Get("X-Icy-Delivery"); got != id.String() {
			t.Errorf("attempt %d X-Icy-Delivery = %q, want %s", i+1, got, id)
		}
	}
	statuses := map[int]int{}
	for _, d := range ds {
		statuses[d.ResponseStatus]++
	}
	if statuses[500] != 1 || statuses[503] != 1 || statuses[200] != 1 {
		t.Errorf("recorded statuses = %v", statuses)
	}
}

func TestQueueGivesUpAfterMaxAttempts(t *testing.T) {
	e := newQueueEnv(t)
	rcv := newReceiver(t, 500, 500, 500, 500)
	hook := e.addWebhook(rcv.URL, "", EventPush)
	w := e.worker()

	e.push()
	for range 3 {
		w.ProcessDue(e.ctx)
		e.advance(time.Hour)
	}
	if rcv.count() != 3 {
		t.Errorf("tried %d times, want max_attempts 3", rcv.count())
	}
	if len(e.queued(hook)) != 0 {
		t.Error("delivery still queued after the last attempt")
	}
	if n := w.ProcessDue(e.ctx); n != 0 {
		t.Errorf("sent %d after giving up", n)
	}
	if got := len(e.delivered(hook)); got != 3 {
		t.Errorf("recorded %d attempts, want 3", got)
	}
}

func TestFailingWebhookDoesNotBlockOthers(t *testing.T) {
	e := newQueueEnv(t)
	good := newReceiver(t)
	bad := newReceiver(t, 500, 500, 500)
	// A closed port: the request fails outright.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()

	deadHook := e.addWebhook(deadURL, "", EventPush)
	badHook := e.addWebhook(bad.URL, "", EventPush)
	goodHook := e.addWebhook(good.URL, "", EventPush)

	e.push()
	if n := e.worker().ProcessDue(e.ctx); n != 3 {
		t.Fatalf("ProcessDue sent %d, want 3", n)
	}
	if good.count() != 1 {
		t.Errorf("good webhook got %d requests, want 1", good.count())
	}
	if len(e.queued(goodHook)) != 0 {
		t.Error("good webhook still queued")
	}
	for name, hook := range map[string]int64{"dead": deadHook, "bad": badHook} {
		if len(e.queued(hook)) != 1 {
			t.Errorf("%s webhook not queued for retry", name)
		}
		ds := e.delivered(hook)
		if len(ds) != 1 {
			t.Errorf("%s webhook recorded %d attempts, want 1", name, len(ds))
		}
	}
	ds := e.delivered(deadHook)
	if len(ds) == 1 && !ds[0].RequestError.Valid {
		t.Error("connection failure not recorded as a request error")
	}
}

func TestSlowEndpointDoesNotBlockPush(t *testing.T) {
	e := newQueueEnv(t)
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(slow.Close)
	t.Cleanup(func() { close(release) })
	hook := e.addWebhook(slow.URL, "", EventPush)

	// This is what the hook process does on push. It must not wait for the
	// receiver at all.
	start := time.Now()
	e.push()
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("queueing a push event took %s", d)
	}
	if len(e.queued(hook)) != 1 {
		t.Fatal("push event not queued")
	}
	if len(e.delivered(hook)) != 0 {
		t.Error("push event sent from the hook path")
	}
}

func TestQueueSurvivesRestart(t *testing.T) {
	e := newQueueEnv(t)
	rcv := newReceiver(t)
	hook := e.addWebhook(rcv.URL, "", EventPush)

	// Queued by one process, which then goes away.
	e.push()
	id := e.queued(hook)[0].ID
	if err := e.db.Close(); err != nil {
		t.Fatal(err)
	}

	// A new server process picks it up.
	e.open()
	if n := e.worker().ProcessDue(e.ctx); n != 1 {
		t.Fatalf("restarted worker sent %d, want 1", n)
	}
	if rcv.count() != 1 || rcv.requests[0].Header.Get("X-Icy-Delivery") != id.String() {
		t.Errorf("restarted worker did not send delivery %s", id)
	}
}

func TestQueueRecoversDeliveryFromCrashedWorker(t *testing.T) {
	e := newQueueEnv(t)
	rcv := newReceiver(t)
	hook := e.addWebhook(rcv.URL, "", EventPush)

	e.push()
	item := e.queued(hook)[0]

	// A worker claims the delivery and dies before it finishes.
	now := e.clock.Load()
	ok, err := e.store.ClaimWebhookQueueItem(e.ctx, e.db, item.ID, item.Attempts, now, now+int64(defaultLease/time.Second))
	if err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}

	// While the claim holds, nobody else sends it.
	w := e.worker()
	if n := w.ProcessDue(e.ctx); n != 0 {
		t.Fatalf("sent %d while another worker held the claim", n)
	}

	// Once the claim runs out it is sent, as the second attempt.
	e.advance(defaultLease)
	if n := w.ProcessDue(e.ctx); n != 1 {
		t.Fatalf("sent %d after the claim ran out, want 1", n)
	}
	ds := e.delivered(hook)
	if len(ds) != 1 || ds[0].ID == item.ID {
		t.Errorf("recovered attempt not logged as a retry: %+v", ds)
	}
	if rcv.requests[0].Header.Get("X-Icy-Delivery") != item.ID.String() {
		t.Error("recovered attempt changed the delivery ID")
	}
}

func TestQueueShutdownReleasesDelivery(t *testing.T) {
	e := newQueueEnv(t)
	started := make(chan struct{}, 1)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the body first: only then does the server notice the
		// client hanging up and cancel the request context.
		io.Copy(io.Discard, r.Body) //nolint:errcheck
		started <- struct{}{}
		<-r.Context().Done()
	}))
	t.Cleanup(slow.Close)
	hook := e.addWebhook(slow.URL, "", EventPush)
	e.push()

	ctx, cancel := context.WithCancel(e.ctx)
	w := e.worker()
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not pick up the delivery")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop")
	}

	// The cut-off attempt is not counted or logged, and the delivery is
	// due again at once.
	q := e.queued(hook)
	if len(q) != 1 || q[0].Attempts != 0 || q[0].LockedUntil != 0 {
		t.Errorf("after shutdown queue = %+v", q)
	}
	if got := len(e.delivered(hook)); got != 0 {
		t.Errorf("recorded %d attempts for a cut-off request", got)
	}
}

func TestQueueClaimIsExclusive(t *testing.T) {
	e := newQueueEnv(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	hook := e.addWebhook(srv.URL, "", EventPush)

	const events = 20
	for range events {
		e.push()
	}

	// Several workers race over the same queue.
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.worker().ProcessDue(e.ctx)
		}()
	}
	wg.Wait()

	if got := hits.Load(); got != events {
		t.Errorf("receiver got %d requests for %d events", got, events)
	}
	if got := len(e.delivered(hook)); got != events {
		t.Errorf("recorded %d deliveries for %d events", got, events)
	}
	if len(e.queued(hook)) != 0 {
		t.Error("deliveries left in the queue")
	}

	// A second claim on the same attempt fails.
	e.push()
	item := e.queued(hook)[0]
	now := e.clock.Load()
	first, err := e.store.ClaimWebhookQueueItem(e.ctx, e.db, item.ID, item.Attempts, now, now+60)
	if err != nil || !first {
		t.Fatalf("first claim = %v, %v", first, err)
	}
	second, err := e.store.ClaimWebhookQueueItem(e.ctx, e.db, item.ID, item.Attempts, now, now+60)
	if err != nil || second {
		t.Errorf("second claim = %v, %v, want false", second, err)
	}
}

func TestQueueSkipsInactiveWebhooks(t *testing.T) {
	e := newQueueEnv(t)
	rcv := newReceiver(t)
	hook := e.addWebhook(rcv.URL, "", EventPush)
	if err := e.store.UpdateWebhookByID(e.ctx, e.db, e.repoID, hook, rcv.URL, "", int(ContentTypeJSON), false); err != nil {
		t.Fatal(err)
	}
	e.push()
	if len(e.queued(hook)) != 0 {
		t.Error("event queued for an inactive webhook")
	}
}

func TestQueueBlocksPrivateAddressesOutsideAllowlist(t *testing.T) {
	e := newQueueEnv(t)
	e.cfg.Webhook.AllowedNetworks = []string{"10.0.0.0/8"}
	if err := e.cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	rcv := newReceiver(t)
	hook := e.addWebhook(rcv.URL, "", EventPush)

	e.push()
	e.worker().ProcessDue(e.ctx)
	if rcv.count() != 0 {
		t.Error("worker reached loopback, which is not allowed")
	}
	ds := e.delivered(hook)
	if len(ds) != 1 || !ds[0].RequestError.Valid {
		t.Fatalf("blocked attempt not recorded as an error: %+v", ds)
	}
}

func TestRedeliverQueuesNewDelivery(t *testing.T) {
	e := newQueueEnv(t)
	rcv := newReceiver(t)
	hook := e.addWebhook(rcv.URL, "", EventPush)
	e.push()
	w := e.worker()
	w.ProcessDue(e.ctx)
	ds := e.delivered(hook)
	if len(ds) != 1 {
		t.Fatalf("recorded %d deliveries, want 1", len(ds))
	}

	wh, err := e.store.GetWebhookByID(e.ctx, e.db, e.repoID, hook)
	if err != nil {
		t.Fatal(err)
	}
	if err := Redeliver(e.ctx, wh, ds[0]); err != nil {
		t.Fatal(err)
	}
	w.ProcessDue(e.ctx)
	if rcv.count() != 2 || rcv.bodies[0] != rcv.bodies[1] {
		t.Fatalf("redelivery did not resend the same body")
	}
	if rcv.requests[1].Header.Get("X-Icy-Delivery") == ds[0].ID.String() {
		t.Error("redelivery reused the delivery ID")
	}
	if _, err := uuid.Parse(rcv.requests[1].Header.Get("X-Icy-Delivery")); err != nil {
		t.Error("redelivery has no delivery ID")
	}
}
