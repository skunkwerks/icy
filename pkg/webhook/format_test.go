package webhook

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const (
	testAfter  = "9a8e47d2c0b1f3e5a7c9d1b3f5e7a9c1d3e5f7a9"
	testBefore = "0f3c1b5a9e7d5c3b1a9f7e5d3c1b9a7f5e3d1c9b"
)

func testPushEvent(repoID int64, ref, after string, commits ...Commit) PushEvent {
	return PushEvent{
		Common: Common{
			EventType:  EventPush,
			Repository: Repository{ID: repoID, Name: "repo"},
		},
		Ref:     ref,
		Before:  testBefore,
		After:   after,
		Commits: commits,
	}
}

func TestBuildkiteBodyGolden(t *testing.T) {
	id := uuid.MustParse("6f1c2d3e-4a5b-4c6d-8e7f-9a0b1c2d3e4f")
	for _, tc := range []struct {
		name    string
		commits []Commit
		want    string
	}{
		{
			name: "plain",
			commits: []Commit{
				{Title: "Fix the build", Author: Author{Name: "Frankie", Email: "frankie@example.com"}},
				{Title: "Older commit", Author: Author{Name: "Someone Else", Email: "else@example.com"}},
			},
			want: `{"commit":"` + testAfter + `","branch":"main","message":"Fix the build","author":{"name":"Frankie","email":"frankie@example.com"},"meta_data":{"icy_delivery":"6f1c2d3e-4a5b-4c6d-8e7f-9a0b1c2d3e4f","icy_repo":"repo"}}` + "\n",
		},
		{
			name: "escaped",
			commits: []Commit{
				{Title: "Say \"hi\" to Zoë ✓\nand\ttab \\ <b>&</b>", Author: Author{Name: "Zoë \"Z\" Ünal", Email: "zoe@example.com"}},
			},
			want: `{"commit":"` + testAfter + `","branch":"main","message":"Say \"hi\" to Zoë ✓\nand\ttab \\ <b>&</b>","author":{"name":"Zoë \"Z\" Ünal","email":"zoe@example.com"},"meta_data":{"icy_delivery":"6f1c2d3e-4a5b-4c6d-8e7f-9a0b1c2d3e4f","icy_repo":"repo"}}` + "\n",
		},
		{
			name: "no commits",
			want: `{"commit":"` + testAfter + `","branch":"main","message":"","author":{"name":"","email":""},"meta_data":{"icy_delivery":"6f1c2d3e-4a5b-4c6d-8e7f-9a0b1c2d3e4f","icy_repo":"repo"}}` + "\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, ok, err := buildkiteBody(testPushEvent(1, "refs/heads/main", testAfter, tc.commits...), id)
			if err != nil || !ok {
				t.Fatalf("buildkiteBody = %v, %v", ok, err)
			}
			if body != tc.want {
				t.Errorf("body =\n%s\nwant\n%s", body, tc.want)
			}
			// And it reads back as the message it was given.
			var got buildkiteBuild
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatal(err)
			}
			if len(tc.commits) > 0 && got.Message != tc.commits[0].Title {
				t.Errorf("message read back as %q", got.Message)
			}
		})
	}
}

func TestBuildkiteBodyBranchNames(t *testing.T) {
	body, ok, err := buildkiteBody(testPushEvent(1, "refs/heads/feature/x", testAfter), uuid.New())
	if err != nil || !ok || !strings.Contains(body, `"branch":"feature/x"`) {
		t.Errorf("buildkiteBody = %q, %v, %v", body, ok, err)
	}
}

func TestBuildkiteBodyTag(t *testing.T) {
	d := uuid.MustParse("6f1c2d3e-4a5b-4c6d-8e7f-9a0b1c2d3e4f")
	// An annotated tag: After names the tag object, the newest commit is
	// the one it points at, and that is the commit to build.
	tagObject := "1111111111111111111111111111111111111111"
	body, ok, err := buildkiteBody(testPushEvent(1, "refs/tags/v1.0.0", tagObject,
		Commit{ID: testAfter, Title: "Release 1.0", Author: Author{Name: "Frankie", Email: "frankie@example.com"}}), d)
	want := `{"commit":"` + testAfter + `","branch":"v1.0.0","message":"Release 1.0","author":{"name":"Frankie","email":"frankie@example.com"},"meta_data":{"icy_delivery":"6f1c2d3e-4a5b-4c6d-8e7f-9a0b1c2d3e4f","icy_repo":"repo","icy_tag":"v1.0.0"}}` + "\n"
	if err != nil || !ok || body != want {
		t.Errorf("annotated tag: buildkiteBody =\n%s\n%v, %v, want\n%s", body, ok, err, want)
	}

	// With no commits to peel, the pushed object is used as it is.
	body, ok, err = buildkiteBody(testPushEvent(1, "refs/tags/v2", testAfter), d)
	if err != nil || !ok || !strings.Contains(body, `"commit":"`+testAfter+`","branch":"v2"`) || !strings.Contains(body, `"icy_tag":"v2"`) {
		t.Errorf("lightweight tag: buildkiteBody = %q, %v, %v", body, ok, err)
	}

	// Branch pushes carry no icy_tag.
	body, _, _ = buildkiteBody(testPushEvent(1, "refs/heads/main", testAfter), d)
	if strings.Contains(body, "icy_tag") {
		t.Errorf("branch push has icy_tag: %s", body)
	}
}

func TestBuildkiteBodySkips(t *testing.T) {
	zero := strings.Repeat("0", 40)
	for name, p := range map[string]PushEvent{
		"branch deletion": testPushEvent(1, "refs/heads/main", zero),
		"tag deletion":    testPushEvent(1, "refs/tags/v1.0.0", zero),
		"other ref":       testPushEvent(1, "refs/notes/commits", testAfter),
	} {
		body, ok, err := buildkiteBody(p, uuid.New())
		if err != nil || ok || body != "" {
			t.Errorf("%s: buildkiteBody = %q, %v, %v, want a skip", name, body, ok, err)
		}
	}
}

func TestValidateFormat(t *testing.T) {
	for _, tc := range []struct {
		format Format
		events []Event
		ct     ContentType
		err    error
	}{
		{FormatIcy, []Event{EventCollaborator}, ContentTypeForm, nil},
		{FormatBuildkite, []Event{EventPush}, ContentTypeJSON, nil},
		{FormatBuildkite, []Event{EventPush, EventBranchTagCreate}, ContentTypeJSON, ErrFormatEvents},
		{FormatBuildkite, []Event{EventRepository}, ContentTypeJSON, ErrFormatEvents},
		{FormatBuildkite, nil, ContentTypeJSON, ErrFormatEvents},
		{FormatBuildkite, []Event{EventPush}, ContentTypeForm, ErrFormatContentType},
		{Format("travis"), []Event{EventPush}, ContentTypeJSON, ErrInvalidFormat},
	} {
		if err := ValidateFormat(tc.format, tc.events, tc.ct); !errors.Is(err, tc.err) {
			t.Errorf("ValidateFormat(%s, %v, %s) = %v, want %v", tc.format, tc.events, tc.ct, err, tc.err)
		}
	}
	if _, err := ParseFormat("Buildkite"); err != nil {
		t.Errorf("ParseFormat(Buildkite) = %v", err)
	}
	if _, err := ParseFormat("gitlab"); !errors.Is(err, ErrInvalidFormat) {
		t.Errorf("ParseFormat(gitlab) = %v", err)
	}
}

func TestSendEventBuildkite(t *testing.T) {
	e := newQueueEnv(t)
	rcv := newReceiver(t, 201, 201, 201)
	bk := e.addWebhook(rcv.URL, "", EventPush)
	if err := e.store.UpdateWebhookFormat(e.ctx, e.db, e.repoID, bk, string(FormatBuildkite)); err != nil {
		t.Fatal(err)
	}
	e.setBearer(bk, "bkua_token")
	icy := e.addWebhook(rcv.URL+"/icy", "", EventPush)

	send := func(p PushEvent) {
		t.Helper()
		if err := SendEvent(e.ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	// Branch deletions queue nothing for buildkite, but still go to the
	// icy webhook.
	send(testPushEvent(e.repoID, "refs/heads/gone", strings.Repeat("0", 40)))
	if n := len(e.queued(bk)); n != 0 {
		t.Errorf("buildkite webhook queued %d deliveries for a deletion", n)
	}
	if n := len(e.queued(icy)); n != 1 {
		t.Errorf("icy webhook queued %d deliveries, want 1", n)
	}

	// A tag push starts a build of the tag.
	send(testPushEvent(e.repoID, "refs/tags/v1", testAfter))
	q := e.queued(bk)
	if len(q) != 1 || !strings.Contains(q[0].RequestBody, `"branch":"v1"`) || !strings.Contains(q[0].RequestBody, `"icy_tag":"v1"`) {
		t.Fatalf("buildkite webhook queued %d deliveries for a tag push, want 1 for v1: %v", len(q), q)
	}
	tagBody := q[0].RequestBody

	send(testPushEvent(e.repoID, "refs/heads/main", testAfter,
		Commit{Title: "Ship it", Author: Author{Name: "Frankie", Email: "frankie@example.com"}}))
	q = e.queued(bk)
	if len(q) != 2 {
		t.Fatalf("buildkite webhook queued %d deliveries, want 2", len(q))
	}
	var mainItem string
	var mainID string
	for _, it := range q {
		if strings.Contains(it.RequestBody, `"branch":"main"`) {
			mainItem, mainID = it.RequestBody, it.ID.String()
		}
	}
	want := `{"commit":"` + testAfter + `","branch":"main","message":"Ship it","author":{"name":"Frankie","email":"frankie@example.com"},"meta_data":{"icy_delivery":"` + mainID + `","icy_repo":"repo"}}` + "\n"
	if mainItem != want {
		t.Errorf("queued body =\n%s\nwant\n%s", mainItem, want)
	}

	e.worker().ProcessDue(e.ctx)
	got := map[string]bool{}
	for i, r := range rcv.requests {
		if r.URL.Path != "/" {
			continue
		}
		if r.Header.Get("Authorization") != "Bearer bkua_token" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("buildkite request headers = %v", r.Header)
		}
		got[rcv.bodies[i]] = true
	}
	if len(got) != 2 || !got[want] || !got[tagBody] {
		t.Errorf("buildkite receiver got %v, want the main and the tag build", got)
	}
	// Buildkite answers 201, which is a success.
	if len(e.queued(bk)) != 0 {
		t.Error("a 201 answer was not taken as success")
	}
}
