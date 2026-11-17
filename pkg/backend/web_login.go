package backend

import (
	"context"
	"crypto/rand"
	"errors"
	"sync"
	"time"

	"github.com/skunkwerks/icy/pkg/proto"
)

// WebLoginTTL is how long a web sign-in link stays valid.
const WebLoginTTL = 5 * time.Minute

// maxWebLogins caps the number of unused sign-in links held in memory.
const maxWebLogins = 10000

// ErrInvalidWebLogin is the one answer for a sign-in token that is unknown,
// already used, or expired.
var ErrInvalidWebLogin = errors.New("invalid or expired sign-in link")

// errTooManyWebLogins is returned when too many sign-in links are pending.
var errTooManyWebLogins = errors.New("too many pending sign-in links, try again later")

// webLogin is a pending sign-in link.
type webLogin struct {
	userID  int64
	next    string
	expires time.Time
}

// webLogins holds pending sign-in links, keyed by the token's hash. They
// live in memory only: a restart drops every pending link, so nothing can
// be replayed across one. Its zero value is ready to use.
type webLogins struct {
	mu sync.Mutex
	m  map[string]webLogin
}

// CreateWebLogin mints a single-use token that signs user in to the web UI.
// It is valid for ttl. next is the path the browser goes to after signing
// in. It is chosen by the server, never read from the sign-in request.
func (d *Backend) CreateWebLogin(_ context.Context, user proto.User, next string, ttl time.Duration) (string, error) {
	if user == nil {
		return "", proto.ErrUserNotFound
	}

	token := "icy_login_" + rand.Text()
	now := time.Now()

	w := &d.webLogins
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.m == nil {
		w.m = make(map[string]webLogin)
	}
	for k, l := range w.m {
		if !now.Before(l.expires) {
			delete(w.m, k)
		}
	}
	if len(w.m) >= maxWebLogins {
		return "", errTooManyWebLogins
	}

	w.m[HashToken(token)] = webLogin{
		userID:  user.ID(),
		next:    next,
		expires: now.Add(ttl),
	}
	return token, nil
}

// RedeemWebLogin uses up a sign-in token and returns the user it signs in
// and the path to go to next. A token works once: it is removed before it
// is checked, so two requests racing with the same token cannot both win.
func (d *Backend) RedeemWebLogin(ctx context.Context, token string) (proto.User, string, error) {
	w := &d.webLogins
	key := HashToken(token)

	w.mu.Lock()
	l, ok := w.m[key]
	delete(w.m, key)
	w.mu.Unlock()

	if !ok || !time.Now().Before(l.expires) {
		return nil, "", ErrInvalidWebLogin
	}

	user, err := d.UserByID(ctx, l.userID)
	if err != nil {
		return nil, "", ErrInvalidWebLogin
	}
	return user, l.next, nil
}
