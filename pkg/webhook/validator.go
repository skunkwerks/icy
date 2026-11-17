package webhook

import (
	"context"
	"errors"
	"net"
	"net/url"

	"github.com/skunkwerks/icy/pkg/ssrf"
)

// Error aliases for backward compatibility.
var (
	ErrInvalidScheme = ssrf.ErrInvalidScheme
	ErrPrivateIP     = ssrf.ErrPrivateIP
	ErrInvalidURL    = ssrf.ErrInvalidURL
)

// ValidateWebhookURL validates that a webhook URL is safe to use.
func ValidateWebhookURL(rawURL string) error {
	return ssrf.ValidateURL(rawURL) //nolint:wrapcheck
}

// ValidateWebhookURLWithAllowlist is ValidateWebhookURL, except that
// addresses in allow are accepted even though they are private or internal.
func ValidateWebhookURLWithAllowlist(rawURL string, allow ssrf.AllowedNetworks) error {
	return ssrf.ValidateURLWithAllowlist(rawURL, allow) //nolint:wrapcheck
}

// ErrBearerNeedsHTTPS is returned when a webhook with a bearer token would
// send it in clear text.
var ErrBearerNeedsHTTPS = errors.New("a webhook with a bearer token must use https, unless its address is in webhook.allowed_networks")

// maxBearerToken caps the length of a bearer token.
const maxBearerToken = 4096

// ErrInvalidBearerToken is returned for an empty or malformed token.
var ErrInvalidBearerToken = errors.New("bearer token must be 1 to 4096 printable characters")

// ValidateBearerToken checks that a token can go in an HTTP header.
func ValidateBearerToken(token string) error {
	if token == "" || len(token) > maxBearerToken {
		return ErrInvalidBearerToken
	}
	for i := 0; i < len(token); i++ {
		if c := token[i]; c < 0x21 || c > 0x7e {
			return ErrInvalidBearerToken
		}
	}
	return nil
}

// ValidateBearerURL checks that a webhook URL may carry a bearer token. The
// URL must use https, or its host must be an address, or resolve only to
// addresses, in allow. Call it after ValidateWebhookURLWithAllowlist.
func ValidateBearerURL(rawURL string, allow ssrf.AllowedNetworks) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ErrInvalidURL
	}
	if u.Scheme == "https" {
		return nil
	}

	host := u.Hostname()
	if host == "" || len(allow) == 0 {
		return ErrBearerNeedsHTTPS
	}
	if ip := net.ParseIP(host); ip != nil {
		if allow.Contains(ip) {
			return nil
		}
		return ErrBearerNeedsHTTPS
	}

	ips, err := net.DefaultResolver.LookupIPAddr(context.Background(), host)
	if err != nil || len(ips) == 0 {
		return ErrBearerNeedsHTTPS
	}
	for _, ip := range ips {
		if !allow.Contains(ip.IP) {
			return ErrBearerNeedsHTTPS
		}
	}
	return nil
}

// ValidateIPBeforeDial validates an IP address before establishing a connection.
var ValidateIPBeforeDial = ssrf.ValidateIPBeforeDial
