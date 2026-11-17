package webhook

import (
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

// ValidateIPBeforeDial validates an IP address before establishing a connection.
var ValidateIPBeforeDial = ssrf.ValidateIPBeforeDial
