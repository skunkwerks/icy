package web

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"charm.land/log/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/skunkwerks/icy/pkg/backend"
	"github.com/skunkwerks/icy/pkg/config"
	"github.com/skunkwerks/icy/pkg/jwk"
	"github.com/skunkwerks/icy/pkg/proto"
)

// Web UI sessions.
//
// `ssh host web login` mints a single-use sign-in token (see
// backend.CreateWebLogin). GET /-/login?token=T redeems it and sets a
// session cookie holding a JWT signed with the server's ed25519 key, the
// same key and code that `icy jwt` uses. The cookie is the only credential
// the web UI reads.
//
// The JWT audience is "~web". It is not a URL. utils.ValidateRepo rejects
// "~" in every repository name, old or new, so a session token never
// passes the audience check of a git bearer token, and
// a git bearer token, whose audience is a repository name, never passes as
// a session.
//
// Session ids start with an epoch picked at random when the server starts.
// Sign-out revocations live in memory, so a restart must end every session
// too, or a signed-out cookie would work again.

const (
	sessionCookie   = "icy_session"
	sessionAudience = "~web"
	// sessionTTL is how long a web session lasts.
	sessionTTL = 24 * time.Hour
)

var errNoSession = errors.New("no valid session")

// mintSession signs a session token for user. Its id starts with the
// server's session epoch, so it stops working when the server restarts.
func (ui *webUI) mintSession(cfg *config.Config, user proto.User) (string, time.Time, error) {
	kp, err := jwk.NewPair(cfg)
	if err != nil {
		return "", time.Time{}, err
	}

	now := time.Now()
	exp := now.Add(sessionTTL)
	claims := jwt.RegisteredClaims{
		ID:        ui.epoch + "." + rand.Text(),
		Subject:   fmt.Sprintf("%s#%d", user.Username(), user.ID()),
		Issuer:    cfg.HTTP.PublicURL,
		Audience:  jwt.ClaimStrings{sessionAudience},
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(exp),
	}
	token := jwt.NewWithClaims(jwk.SigningMethod, claims)
	token.Header["kid"] = kp.JWK().KeyID
	signed, err := token.SignedString(kp.PrivateKey())
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, exp, nil
}

// parseSession verifies the session cookie on r and returns its claims.
func (ui *webUI) parseSession(r *http.Request) (*jwt.RegisteredClaims, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, errNoSession
	}

	cfg := config.FromContext(r.Context())
	kp, err := config.KeyPair(cfg)
	if err != nil {
		return nil, err
	}

	claims := &jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(c.Value, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, errors.New("invalid signing method")
		}
		return kp.CryptoPublicKey(), nil
	},
		jwt.WithValidMethods([]string{jwk.SigningMethod.Alg()}),
		jwt.WithIssuer(cfg.HTTP.PublicURL),
		jwt.WithAudience(sessionAudience),
		jwt.WithIssuedAt(),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !token.Valid || !strings.HasPrefix(claims.ID, ui.epoch+".") {
		return nil, errNoSession
	}
	if ui.revoked.has(claims.ID) {
		return nil, errNoSession
	}
	return claims, nil
}

// session returns the signed-in user, or nil. A bad, expired or revoked
// cookie is the same as no cookie.
func (ui *webUI) session(r *http.Request) (proto.User, *jwt.RegisteredClaims) {
	claims, err := ui.parseSession(r)
	if err != nil {
		return nil, nil
	}

	username, id, ok := strings.Cut(claims.Subject, "#")
	if !ok {
		return nil, nil
	}
	ctx := r.Context()
	user, err := backend.FromContext(ctx).User(ctx, username)
	if err != nil || fmt.Sprintf("%s#%d", user.Username(), user.ID()) != username+"#"+id {
		return nil, nil
	}
	return user, claims
}

// secureCookies reports whether cookies should carry the Secure flag: when
// the request came over TLS or the public URL is https, as it is behind a
// TLS-terminating proxy.
func secureCookies(r *http.Request) bool {
	cfg := config.FromContext(r.Context())
	return r.TLS != nil || strings.HasPrefix(cfg.HTTP.PublicURL, "https://")
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, value string, exp time.Time) {
	c := &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookies(r),
		SameSite: http.SameSiteLaxMode,
	}
	if value == "" {
		c.MaxAge = -1
	} else {
		c.Expires = exp
		c.MaxAge = int(time.Until(exp).Seconds())
	}
	http.SetCookie(w, c)
}

// safeNext keeps a post-login redirect on this site.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\\\r\n") {
		return "/"
	}
	return next
}

// login swaps a single-use sign-in token for a session cookie.
func (ui *webUI) login(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cfg := config.FromContext(ctx)
	be := backend.FromContext(ctx)
	w.Header().Set("Cache-Control", "no-store")

	user, next, err := be.RedeemWebLogin(ctx, r.URL.Query().Get("token"))
	if err != nil {
		ui.renderError(w, r, http.StatusBadRequest,
			"This sign-in link is not valid. It may have been used already or have expired. Run the web login command again for a new one.")
		return
	}

	value, exp, err := ui.mintSession(cfg, user)
	if err != nil {
		log.FromContext(ctx).Error("failed to mint web session", "err", err)
		ui.renderError(w, r, http.StatusInternalServerError, "Signing in failed.")
		return
	}

	setSessionCookie(w, r, value, exp)
	http.Redirect(w, r, safeNext(next), http.StatusSeeOther)
}

// sameOrigin reports whether a state-changing request came from this site.
// Requests without an Origin header, such as from older browsers or curl,
// are allowed: the session cookie is SameSite=Lax, so a cross-site form
// post does not carry it anyway.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	o, err := url.Parse(origin)
	if err != nil {
		return false
	}
	cfg := config.FromContext(r.Context())
	if pub, err := url.Parse(cfg.HTTP.PublicURL); err == nil && o.Scheme == pub.Scheme && o.Host == pub.Host {
		return true
	}
	return o.Host == r.Host
}

// logout ends the session: the token is revoked on the server and the
// cookie is cleared.
func (ui *webUI) logout(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		ui.renderError(w, r, http.StatusForbidden, "Sign out from this site's own page.")
		return
	}
	if claims, err := ui.parseSession(r); err == nil {
		ui.revoked.add(claims.ID, claims.ExpiresAt.Time)
	}
	w.Header().Set("Cache-Control", "no-store")
	setSessionCookie(w, r, "", time.Time{})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// revocations remembers signed-out session ids until they expire. It is
// kept in memory, which is safe because a restart also changes the session
// epoch and so ends every session.
type revocations struct {
	mu sync.Mutex
	m  map[string]time.Time
}

func newRevocations() *revocations {
	return &revocations{m: make(map[string]time.Time)}
}

func (v *revocations) add(id string, exp time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := time.Now()
	for k, e := range v.m {
		if now.After(e) {
			delete(v.m, k)
		}
	}
	v.m[id] = exp
}

func (v *revocations) has(id string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, ok := v.m[id]
	return ok
}
