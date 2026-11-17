package webhook

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/skunkwerks/icy/pkg/secrets"
	"github.com/skunkwerks/icy/pkg/ssrf"
)

// setBearer seals token with the secrets key in the data directory, making
// the key if needed, and stores it on the webhook. An empty token clears it.
func (e *queueEnv) setBearer(hook int64, token string) {
	e.t.Helper()
	sealed := ""
	if token != "" {
		key, _, err := secrets.LoadOrCreateKey(secrets.KeyPath(e.cfg.DataPath))
		if err != nil {
			e.t.Fatal(err)
		}
		if sealed, err = secrets.Encrypt(key, []byte(token)); err != nil {
			e.t.Fatal(err)
		}
	}
	if err := e.store.UpdateWebhookBearerToken(e.ctx, e.db, e.repoID, hook, sealed); err != nil {
		e.t.Fatal(err)
	}
}

func TestBearerTokenSentAndRedacted(t *testing.T) {
	e := newQueueEnv(t)
	rcv := newReceiver(t)
	hook := e.addWebhook(rcv.URL, "s3cret", EventPush)
	e.setBearer(hook, "tok-123")

	e.push()
	w := e.worker()
	if n := w.ProcessDue(e.ctx); n != 1 {
		t.Fatalf("ProcessDue sent %d, want 1", n)
	}
	req := rcv.requests[0]
	if got := req.Header.Get("Authorization"); got != "Bearer tok-123" {
		t.Errorf("Authorization = %q, want Bearer tok-123", got)
	}
	if req.Header.Get("X-Icy-Signature") == "" {
		t.Error("X-Icy-Signature dropped when a bearer token is set")
	}

	ds := e.delivered(hook)
	if len(ds) != 1 {
		t.Fatalf("recorded %d deliveries, want 1", len(ds))
	}
	if !strings.Contains(ds[0].RequestHeaders, "Authorization: Bearer [REDACTED]\n") {
		t.Errorf("delivery log headers = %q, want the token redacted", ds[0].RequestHeaders)
	}
	if strings.Contains(ds[0].RequestHeaders, "tok-123") {
		t.Errorf("delivery log holds the token: %q", ds[0].RequestHeaders)
	}

	// A redelivery still sends the right content type and the token.
	wh, err := e.store.GetWebhookByID(e.ctx, e.db, e.repoID, hook)
	if err != nil {
		t.Fatal(err)
	}
	if err := Redeliver(e.ctx, wh, ds[0]); err != nil {
		t.Fatal(err)
	}
	w.ProcessDue(e.ctx)
	if rcv.count() != 2 {
		t.Fatalf("receiver got %d requests, want 2", rcv.count())
	}
	if got := rcv.requests[1].Header.Get("Authorization"); got != "Bearer tok-123" {
		t.Errorf("redelivery Authorization = %q", got)
	}
	if got := rcv.requests[1].Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("redelivery Content-Type = %q", got)
	}
}

func TestNoRetryOnRefusedCredentials(t *testing.T) {
	for _, tc := range []struct {
		status int
		retry  bool
	}{
		{http.StatusUnauthorized, false},
		{http.StatusForbidden, false},
		{http.StatusInternalServerError, true},
		{http.StatusNotFound, true},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			e := newQueueEnv(t)
			rcv := newReceiver(t, tc.status, tc.status, tc.status)
			hook := e.addWebhook(rcv.URL, "", EventPush)
			e.setBearer(hook, "tok")
			w := e.worker()

			e.push()
			w.ProcessDue(e.ctx)
			ds := e.delivered(hook)
			if len(ds) != 1 || ds[0].ResponseStatus != tc.status {
				t.Fatalf("recorded %+v, want one attempt with status %d", ds, tc.status)
			}
			if got := len(e.queued(hook)) == 1; got != tc.retry {
				t.Errorf("queued for retry = %v, want %v", got, tc.retry)
			}

			e.advance(time.Hour)
			w.ProcessDue(e.ctx)
			want := 1
			if tc.retry {
				want = 2
			}
			if rcv.count() != want {
				t.Errorf("receiver got %d requests, want %d", rcv.count(), want)
			}
		})
	}
}

func TestBearerNotForwardedOnRedirect(t *testing.T) {
	e := newQueueEnv(t)
	target := newReceiver(t)
	var gotAuth []string
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
		http.Redirect(w, r, target.URL+"/stolen", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirector.Close)

	hook := e.addWebhook(redirector.URL, "", EventPush)
	e.setBearer(hook, "tok")
	e.push()
	e.worker().ProcessDue(e.ctx)

	if len(gotAuth) != 1 || gotAuth[0] != "Bearer tok" {
		t.Errorf("redirector got Authorization %q", gotAuth)
	}
	if target.count() != 0 {
		t.Errorf("redirect was followed, target got %d requests", target.count())
	}
	ds := e.delivered(hook)
	if len(ds) != 1 || ds[0].ResponseStatus != http.StatusTemporaryRedirect {
		t.Errorf("recorded %+v, want one 307 attempt", ds)
	}
}

func TestBearerChangeTakesEffectOnNextAttempt(t *testing.T) {
	e := newQueueEnv(t)
	rcv := newReceiver(t, 500, 500)
	hook := e.addWebhook(rcv.URL, "", EventPush)
	e.setBearer(hook, "first")
	w := e.worker()

	e.push()
	w.ProcessDue(e.ctx)

	e.setBearer(hook, "second")
	e.advance(time.Hour)
	w.ProcessDue(e.ctx)

	e.setBearer(hook, "")
	e.advance(time.Hour)
	w.ProcessDue(e.ctx)

	if rcv.count() != 3 {
		t.Fatalf("receiver got %d requests, want 3", rcv.count())
	}
	for i, want := range []string{"Bearer first", "Bearer second", ""} {
		if got := rcv.requests[i].Header.Get("Authorization"); got != want {
			t.Errorf("attempt %d Authorization = %q, want %q", i+1, got, want)
		}
	}
	ds := e.delivered(hook)
	if len(ds) != 3 || strings.Contains(ds[2].RequestHeaders, "Authorization") {
		t.Errorf("attempt after clearing logged an Authorization header: %+v", ds)
	}
}

func TestBearerNeverSentWithoutKey(t *testing.T) {
	for name, breakKey := range map[string]func(path string) error{
		"missing": os.Remove,
		"wrong": func(path string) error {
			if err := os.Remove(path); err != nil {
				return err
			}
			key, err := secrets.NewKey()
			if err != nil {
				return err
			}
			return secrets.WriteKey(path, key)
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newQueueEnv(t)
			rcv := newReceiver(t)
			hook := e.addWebhook(rcv.URL, "", EventPush)
			e.setBearer(hook, "tok")
			if err := breakKey(secrets.KeyPath(e.cfg.DataPath)); err != nil {
				t.Fatal(err)
			}

			e.push()
			e.worker().ProcessDue(e.ctx)
			if rcv.count() != 0 {
				t.Fatal("delivery sent without its bearer token")
			}
			ds := e.delivered(hook)
			if len(ds) != 1 || ds[0].RequestError.String != errBearerUnavailable.Error() {
				t.Fatalf("recorded %+v, want the bearer error", ds)
			}
			if len(e.queued(hook)) != 1 {
				t.Error("delivery not kept for a retry once the key is back")
			}
		})
	}
}

func TestValidateBearerURL(t *testing.T) {
	loopback, err := ssrf.ParseAllowedNetworks([]string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		url   string
		allow ssrf.AllowedNetworks
		ok    bool
	}{
		{"https://1.1.1.1/hook", nil, true},
		{"https://api.buildkite.com/v2/organizations/o/pipelines/p/builds", nil, true},
		{"http://1.1.1.1/hook", nil, false},
		{"http://1.1.1.1/hook", loopback, false},
		{"http://127.0.0.1:8080/hook", nil, false},
		{"http://127.0.0.1:8080/hook", loopback, true},
		{"http://127.0.0.2:8080/hook", loopback, false},
	} {
		err := ValidateBearerURL(tc.url, tc.allow)
		if (err == nil) != tc.ok {
			t.Errorf("ValidateBearerURL(%q, %v) = %v, want ok %v", tc.url, tc.allow, err, tc.ok)
		}
	}
}

func TestValidateBearerToken(t *testing.T) {
	for _, tc := range []struct {
		token string
		ok    bool
	}{
		{"bkua_0123456789abcdef", true},
		{"a.b-c_d~e+f/g=", true},
		{"", false},
		{"two words", false},
		{"line\nbreak", false},
		{"tab\t", false},
		{"ünï", false},
		{strings.Repeat("a", 4096), true},
		{strings.Repeat("a", 4097), false},
	} {
		if err := ValidateBearerToken(tc.token); (err == nil) != tc.ok {
			t.Errorf("ValidateBearerToken(%.20q) = %v, want ok %v", tc.token, err, tc.ok)
		}
	}
}
