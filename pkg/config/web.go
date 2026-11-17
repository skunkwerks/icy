package config

// WebConfig is the configuration for the read-only web UI served by the
// HTTP server.
type WebConfig struct {
	// Enabled toggles the web UI on/off. The HTTP server must be enabled
	// too.
	Enabled bool `env:"ENABLED" yaml:"enabled"`

	// Owner names who runs the server, such as a person, a team or a
	// contact address. It is shown on the front page.
	Owner string `env:"OWNER" yaml:"owner"`

	// Description is a short text shown on the front page.
	Description string `env:"DESCRIPTION" yaml:"description"`
}
