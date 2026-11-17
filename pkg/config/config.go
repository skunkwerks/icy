package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/skunkwerks/icy/pkg/access"
	"github.com/skunkwerks/icy/pkg/sshutils"
	"github.com/skunkwerks/icy/pkg/ssrf"
	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

var binPath = "icy"

const (
	// StrictHostKeyCheckingAcceptNew records an unknown host key on first
	// contact and refuses a changed one afterwards.
	StrictHostKeyCheckingAcceptNew = "accept-new"
	// StrictHostKeyCheckingYes refuses any host not already in known_hosts.
	StrictHostKeyCheckingYes = "yes"
)

// SSHConfig is the configuration for the SSH server.
type SSHConfig struct {
	// Enabled toggles the SSH server on/off
	Enabled bool `env:"ENABLED" yaml:"enabled"`

	// ListenAddr is the address on which the SSH server will listen.
	ListenAddr string `env:"LISTEN_ADDR" yaml:"listen_addr"`

	// PublicURL is the public URL of the SSH server.
	PublicURL string `env:"PUBLIC_URL" yaml:"public_url"`

	// KeyPath is the path to the SSH server's private key.
	KeyPath string `env:"KEY_PATH" yaml:"key_path"`

	// ClientKeyPath is the path to the server's client private key.
	ClientKeyPath string `env:"CLIENT_KEY_PATH" yaml:"client_key_path"`

	// ClientStrictHostKeyChecking is the StrictHostKeyChecking value the
	// server passes to ssh when cloning or syncing from ssh remotes. Valid
	// values are "accept-new" (record a host key on first contact, refuse a
	// changed one) and "yes" (only hosts already in known_hosts).
	ClientStrictHostKeyChecking string `env:"CLIENT_STRICT_HOST_KEY_CHECKING" yaml:"client_strict_host_key_checking"`

	// ClientAllowedHosts lists the ssh hosts users may import from or mirror.
	// The client key is presented to every one of them. Entries are
	// hostnames, IP addresses, or CIDR ranges with an optional port. When
	// empty, only server admins may use ssh remotes.
	ClientAllowedHosts []string `env:"CLIENT_ALLOWED_HOSTS" yaml:"client_allowed_hosts"`

	// MaxTimeout is the maximum number of seconds a connection can take.
	MaxTimeout int `env:"MAX_TIMEOUT" yaml:"max_timeout"`

	// IdleTimeout is the number of seconds a connection can be idle before it is closed.
	IdleTimeout int `env:"IDLE_TIMEOUT" yaml:"idle_timeout"`

	// allowedHosts is ClientAllowedHosts parsed by Validate.
	allowedHosts ssrf.AllowedHosts
}

// AllowedHosts returns the parsed ssh allowlist. It is empty until Validate
// has run.
func (c *SSHConfig) AllowedHosts() ssrf.AllowedHosts {
	return c.allowedHosts
}

// GitConfig is the Git daemon configuration for the server.
type GitConfig struct {
	// Enabled toggles the Git daemon on/off
	Enabled bool `env:"ENABLED" yaml:"enabled"`

	// ListenAddr is the address on which the Git daemon will listen.
	ListenAddr string `env:"LISTEN_ADDR" yaml:"listen_addr"`

	// PublicURL is the public URL of the Git daemon server.
	PublicURL string `env:"PUBLIC_URL" yaml:"public_url"`

	// MaxTimeout is the maximum number of seconds a connection can take.
	MaxTimeout int `env:"MAX_TIMEOUT" yaml:"max_timeout"`

	// IdleTimeout is the number of seconds a connection can be idle before it is closed.
	IdleTimeout int `env:"IDLE_TIMEOUT" yaml:"idle_timeout"`

	// MaxConnections is the maximum number of concurrent connections.
	MaxConnections int `env:"MAX_CONNECTIONS" yaml:"max_connections"`
}

// CORSConfig is the CORS configuration for the server.
type CORSConfig struct {
	AllowedHeaders []string `env:"ALLOWED_HEADERS" yaml:"allowed_headers"`

	AllowedOrigins []string `env:"ALLOWED_ORIGINS" yaml:"allowed_origins"`

	AllowedMethods []string `env:"ALLOWED_METHODS" yaml:"allowed_methods"`
}

// HTTPConfig is the HTTP configuration for the server.
type HTTPConfig struct {
	// Enabled toggles the HTTP server on/off
	Enabled bool `env:"ENABLED" yaml:"enabled"`

	// ListenAddr is the address on which the HTTP server will listen.
	ListenAddr string `env:"LISTEN_ADDR" yaml:"listen_addr"`

	// TLSKeyPath is the path to the TLS private key.
	TLSKeyPath string `env:"TLS_KEY_PATH" yaml:"tls_key_path"`

	// TLSCertPath is the path to the TLS certificate.
	TLSCertPath string `env:"TLS_CERT_PATH" yaml:"tls_cert_path"`

	// PublicURL is the public URL of the HTTP server.
	PublicURL string `env:"PUBLIC_URL" yaml:"public_url"`

	// CORS is the cross-origin configuration for the HTTP server.
	CORS CORSConfig `envPrefix:"CORS_" yaml:"cors"`
}

// StatsConfig is the configuration for the stats server.
type StatsConfig struct {
	// Enabled toggles the Stats server on/off
	Enabled bool `env:"ENABLED" yaml:"enabled"`

	// ListenAddr is the address on which the stats server will listen.
	ListenAddr string `env:"LISTEN_ADDR" yaml:"listen_addr"`
}

// LogConfig is the logger configuration.
type LogConfig struct {
	// Format is the format of the logs.
	// Valid values are "json", "logfmt", and "text".
	Format string `env:"FORMAT" yaml:"format"`

	// Time format for the log `ts` field.
	// Format must be described in Golang's time format.
	TimeFormat string `env:"TIME_FORMAT" yaml:"time_format"`

	// Path to a file to write logs to.
	// If not set, logs will be written to stderr.
	Path string `env:"PATH" yaml:"path"`
}

// DBConfig is the database connection configuration.
type DBConfig struct {
	// Driver is the driver for the database.
	Driver string `env:"DRIVER" yaml:"driver"`

	// DataSource is the database data source name.
	DataSource string `env:"DATA_SOURCE" yaml:"data_source"`
}

// LFSConfig is the configuration for Git LFS.
type LFSConfig struct {
	// Enabled is whether or not Git LFS is enabled.
	Enabled bool `env:"ENABLED" yaml:"enabled"`

	// SSHEnabled is whether or not Git LFS over SSH is enabled.
	// This is only used if LFS is enabled.
	SSHEnabled bool `env:"SSH_ENABLED" yaml:"ssh_enabled"`
}

// JobsConfig is the configuration for cron jobs.
type JobsConfig struct {
	MirrorPull string `env:"MIRROR_PULL" yaml:"mirror_pull"`
}

// WebhookConfig is the configuration for repository webhooks.
type WebhookConfig struct {
	// AllowedNetworks lists IP prefixes or single addresses that webhooks
	// may reach even though they are private or internal, such as a CI
	// server on the LAN. Hostnames are not accepted.
	AllowedNetworks []string `env:"ALLOWED_NETWORKS" yaml:"allowed_networks"`

	// MaxAttempts is how many times a delivery is tried before it is
	// given up.
	MaxAttempts int `env:"MAX_ATTEMPTS" yaml:"max_attempts"`

	// RetryBackoff is the wait after the first failed attempt. It doubles
	// after each further failure.
	RetryBackoff time.Duration `env:"RETRY_BACKOFF" yaml:"retry_backoff"`

	// allowedNetworks is AllowedNetworks parsed by Validate.
	allowedNetworks ssrf.AllowedNetworks
}

// Allowlist returns the parsed webhook network allowlist. It is empty until
// Validate has run.
func (c *WebhookConfig) Allowlist() ssrf.AllowedNetworks {
	return c.allowedNetworks
}

// Values for Config.StateSource.
const (
	// StateSourceDB keeps users, repositories and access in the database
	// and lets ssh commands change them.
	StateSourceDB = "db"
	// StateSourceConfig reads them from the users, repos, anon_access and
	// allow_keyless keys of config.yaml and makes the database match.
	StateSourceConfig = "config"
)

// Config is the configuration for Icy.
type Config struct {
	// Name is the name of the server.
	Name string `env:"NAME" yaml:"name"`

	// SSH is the configuration for the SSH server.
	SSH SSHConfig `envPrefix:"SSH_" yaml:"ssh"`

	// Git is the configuration for the Git daemon.
	Git GitConfig `envPrefix:"GIT_" yaml:"git"`

	// HTTP is the configuration for the HTTP server.
	HTTP HTTPConfig `envPrefix:"HTTP_" yaml:"http"`

	// Stats is the configuration for the stats server.
	Stats StatsConfig `envPrefix:"STATS_" yaml:"stats"`

	// Log is the logger configuration.
	Log LogConfig `envPrefix:"LOG_" yaml:"log"`

	// DB is the database configuration.
	DB DBConfig `envPrefix:"DB_" yaml:"db"`

	// LFS is the configuration for Git LFS.
	LFS LFSConfig `envPrefix:"LFS_" yaml:"lfs"`

	// Jobs is the configuration for cron jobs
	Jobs JobsConfig `envPrefix:"JOBS_" yaml:"jobs"`

	// Web is the configuration for the web UI.
	Web WebConfig `envPrefix:"WEB_" yaml:"web"`
	// Webhook is the configuration for repository webhooks.
	Webhook WebhookConfig `envPrefix:"WEBHOOK_" yaml:"webhook"`
	// StateSource selects where users, repositories and access settings
	// come from: StateSourceDB (the default) or StateSourceConfig. The
	// users and repos keys are read by package statefile, not here.
	StateSource string `env:"STATE_SOURCE" yaml:"state_source"`

	// InitialAdminKeys is a list of public keys that will be added to the list of admins.
	InitialAdminKeys []string `env:"INITIAL_ADMIN_KEYS" envSeparator:"\n" yaml:"initial_admin_keys"`

	// AnonAccess overrides the access level for anonymous users.
	//
	// If set (non-nil), this takes precedence over the "anon-access" value
	// stored in the database (see the `settings` command) on every read, for
	// as long as it remains set. It is not a one-time default: it behaves
	// like InitialAdminKeys, not like a seed value. Invalid values are
	// rejected at parse time by AccessLevel's TextUnmarshaler.
	//
	// This is intended for scripted, non-production bootstrapping only.
	//
	// With StateSource set to StateSourceConfig the backend does not read
	// this field. The state reconcile writes the effective value
	// (ICY_ANON_ACCESS, else config.yaml, else the default) into the
	// database instead, so that a reload can change it.
	AnonAccess *access.AccessLevel `env:"ANON_ACCESS" yaml:"anon_access"`

	// AllowKeyless overrides whether keyless (no public key) connections
	// are allowed.
	//
	// If set (non-nil), this takes precedence over the "allow-keyless"
	// value stored in the database on every read, same as AnonAccess above.
	// A nil value means "no override": fall back to the database. With
	// StateSourceConfig it is handled like AnonAccess.
	AllowKeyless *bool `env:"ALLOW_KEYLESS" yaml:"allow_keyless"`

	// DefaultRepo is a repository name to create on boot if it does not
	// already exist. Leave empty to disable.
	DefaultRepo string `env:"DEFAULT_REPO" yaml:"default_repo"`

	// DataPath is the path to the directory where Icy will store its data.
	DataPath string `env:"DATA_PATH" yaml:"-"`
}

// Environ returns the config as a list of environment variables.
func (c *Config) Environ() []string {
	envs := []string{
		fmt.Sprintf("ICY_BIN_PATH=%s", binPath),
	}
	if c == nil {
		return envs
	}

	// TODO: do this dynamically
	envs = append(envs, []string{
		fmt.Sprintf("ICY_CONFIG_LOCATION=%s", c.ConfigPath()),
		fmt.Sprintf("ICY_DATA_PATH=%s", c.DataPath),
		fmt.Sprintf("ICY_NAME=%s", c.Name),
		fmt.Sprintf("ICY_INITIAL_ADMIN_KEYS=%s", strings.Join(c.InitialAdminKeys, "\n")),
		fmt.Sprintf("ICY_DEFAULT_REPO=%s", c.DefaultRepo),
		fmt.Sprintf("ICY_SSH_ENABLED=%t", c.SSH.Enabled),
		fmt.Sprintf("ICY_SSH_LISTEN_ADDR=%s", c.SSH.ListenAddr),
		fmt.Sprintf("ICY_SSH_PUBLIC_URL=%s", c.SSH.PublicURL),
		fmt.Sprintf("ICY_SSH_KEY_PATH=%s", c.SSH.KeyPath),
		fmt.Sprintf("ICY_SSH_CLIENT_KEY_PATH=%s", c.SSH.ClientKeyPath),
		fmt.Sprintf("ICY_SSH_CLIENT_STRICT_HOST_KEY_CHECKING=%s", c.SSH.ClientStrictHostKeyChecking),
		fmt.Sprintf("ICY_SSH_CLIENT_ALLOWED_HOSTS=%s", strings.Join(c.SSH.ClientAllowedHosts, ",")),
		fmt.Sprintf("ICY_SSH_MAX_TIMEOUT=%d", c.SSH.MaxTimeout),
		fmt.Sprintf("ICY_SSH_IDLE_TIMEOUT=%d", c.SSH.IdleTimeout),
		fmt.Sprintf("ICY_GIT_ENABLED=%t", c.Git.Enabled),
		fmt.Sprintf("ICY_GIT_LISTEN_ADDR=%s", c.Git.ListenAddr),
		fmt.Sprintf("ICY_GIT_PUBLIC_URL=%s", c.Git.PublicURL),
		fmt.Sprintf("ICY_GIT_MAX_TIMEOUT=%d", c.Git.MaxTimeout),
		fmt.Sprintf("ICY_GIT_IDLE_TIMEOUT=%d", c.Git.IdleTimeout),
		fmt.Sprintf("ICY_GIT_MAX_CONNECTIONS=%d", c.Git.MaxConnections),
		fmt.Sprintf("ICY_HTTP_ENABLED=%t", c.HTTP.Enabled),
		fmt.Sprintf("ICY_HTTP_LISTEN_ADDR=%s", c.HTTP.ListenAddr),
		fmt.Sprintf("ICY_HTTP_TLS_KEY_PATH=%s", c.HTTP.TLSKeyPath),
		fmt.Sprintf("ICY_HTTP_TLS_CERT_PATH=%s", c.HTTP.TLSCertPath),
		fmt.Sprintf("ICY_HTTP_PUBLIC_URL=%s", c.HTTP.PublicURL),
		fmt.Sprintf("ICY_HTTP_CORS_ALLOWED_HEADERS=%s", strings.Join(c.HTTP.CORS.AllowedHeaders, ",")),
		fmt.Sprintf("ICY_HTTP_CORS_ALLOWED_ORIGINS=%s", strings.Join(c.HTTP.CORS.AllowedOrigins, ",")),
		fmt.Sprintf("ICY_HTTP_CORS_ALLOWED_METHODS=%s", strings.Join(c.HTTP.CORS.AllowedMethods, ",")),
		fmt.Sprintf("ICY_STATS_ENABLED=%t", c.Stats.Enabled),
		fmt.Sprintf("ICY_STATS_LISTEN_ADDR=%s", c.Stats.ListenAddr),
		fmt.Sprintf("ICY_LOG_FORMAT=%s", c.Log.Format),
		fmt.Sprintf("ICY_LOG_TIME_FORMAT=%s", c.Log.TimeFormat),
		fmt.Sprintf("ICY_DB_DRIVER=%s", c.DB.Driver),
		fmt.Sprintf("ICY_DB_DATA_SOURCE=%s", c.DB.DataSource),
		fmt.Sprintf("ICY_LFS_ENABLED=%t", c.LFS.Enabled),
		fmt.Sprintf("ICY_LFS_SSH_ENABLED=%t", c.LFS.SSHEnabled),
		fmt.Sprintf("ICY_JOBS_MIRROR_PULL=%s", c.Jobs.MirrorPull),
		fmt.Sprintf("ICY_WEB_ENABLED=%t", c.Web.Enabled),
		fmt.Sprintf("ICY_WEB_OWNER=%s", c.Web.Owner),
		fmt.Sprintf("ICY_WEB_DESCRIPTION=%s", c.Web.Description),
		fmt.Sprintf("ICY_WEBHOOK_ALLOWED_NETWORKS=%s", strings.Join(c.Webhook.AllowedNetworks, ",")),
		fmt.Sprintf("ICY_WEBHOOK_MAX_ATTEMPTS=%d", c.Webhook.MaxAttempts),
		fmt.Sprintf("ICY_WEBHOOK_RETRY_BACKOFF=%s", c.Webhook.RetryBackoff),
		fmt.Sprintf("ICY_STATE_SOURCE=%s", c.StateSource),
	}...)

	// AnonAccess and AllowKeyless are tri-state overrides: only emit them
	// when explicitly set, so a subprocess parsing these envs sees the same
	// "unset" state (rather than an empty string coercing to a zero value).
	if c.AnonAccess != nil {
		envs = append(envs, fmt.Sprintf("ICY_ANON_ACCESS=%s", c.AnonAccess.String()))
	}

	if c.AllowKeyless != nil {
		envs = append(envs, fmt.Sprintf("ICY_ALLOW_KEYLESS=%t", *c.AllowKeyless))
	}

	return envs
}

// IsDebug returns true if the server is running in debug mode.
func IsDebug() bool {
	debug, _ := strconv.ParseBool(os.Getenv("ICY_DEBUG"))
	return debug
}

// IsVerbose returns true if the server is running in verbose mode.
// Verbose mode is only enabled if debug mode is enabled.
func IsVerbose() bool {
	verbose, _ := strconv.ParseBool(os.Getenv("ICY_VERBOSE"))
	return IsDebug() && verbose
}

// parseFile parses the given file as a configuration file.
// The file must be in YAML format.
func parseFile(cfg *Config, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}

	defer f.Close() //nolint: errcheck
	if err := yaml.NewDecoder(f).Decode(cfg); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}

	return cfg.Validate()
}

// ParseFile parses the config from the default file path.
// This also calls Validate() on the config.
func (c *Config) ParseFile() error {
	return parseFile(c, c.ConfigPath())
}

// parseEnv parses the environment variables as a configuration file.
func parseEnv(cfg *Config) error {
	// Merge initial admin keys from both config file and environment variables.
	initialAdminKeys := append([]string{}, cfg.InitialAdminKeys...)

	// Override with environment variables
	if err := env.ParseWithOptions(cfg, env.Options{
		Prefix: "ICY_",
	}); err != nil {
		return fmt.Errorf("parse environment variables: %w", err)
	}

	// Merge initial admin keys from environment variables.
	if initialAdminKeysEnv := os.Getenv("ICY_INITIAL_ADMIN_KEYS"); initialAdminKeysEnv != "" {
		cfg.InitialAdminKeys = append(cfg.InitialAdminKeys, initialAdminKeys...)
	}

	return cfg.Validate()
}

// ParseEnv parses the config from the environment variables.
// This also calls Validate() on the config.
func (c *Config) ParseEnv() error {
	return parseEnv(c)
}

// Parse parses the config from the default file path and environment variables.
// This also calls Validate() on the config.
func (c *Config) Parse() error {
	if err := c.ParseFile(); err != nil {
		return err
	}

	return c.ParseEnv()
}

// writeConfig writes the configuration to the given file.
func writeConfig(cfg *Config, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), os.ModePerm); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(newConfigFile(cfg)), 0o644) //nolint: errcheck, gosec
}

// WriteConfig writes the configuration to the default file.
func (c *Config) WriteConfig() error {
	return writeConfig(c, c.ConfigPath())
}

// DefaultDataPath returns the path to the data directory.
// It uses the ICY_DATA_PATH environment variable if set, otherwise it
// uses "data".
func DefaultDataPath() string {
	dp := os.Getenv("ICY_DATA_PATH")
	if dp == "" {
		dp = "data"
	}

	return dp
}

// ConfigPath returns the path to the config file.
func (c *Config) ConfigPath() string { //nolint:revive
	// If we have a custom config location set, then use that.
	if path := os.Getenv("ICY_CONFIG_LOCATION"); exist(path) {
		return path
	}

	// Otherwise, look in the data path.
	return filepath.Join(c.DataPath, "config.yaml")
}

func exist(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Exist returns true if the config file exists.
func (c *Config) Exist() bool {
	return exist(c.ConfigPath())
}

// DefaultConfig returns the default Config. All the path values are relative
// to the data directory.
// Use Validate() to validate the config and ensure absolute paths.
func DefaultConfig() *Config {
	return &Config{
		Name: "Icy",
		// DefaultRepo: "",
		DataPath: DefaultDataPath(),
		SSH: SSHConfig{
			Enabled:                     true,
			ListenAddr:                  ":23231",
			PublicURL:                   "ssh://localhost:23231",
			KeyPath:                     filepath.Join("ssh", "icy_host_ed25519"),
			ClientKeyPath:               filepath.Join("ssh", "icy_client_ed25519"),
			ClientStrictHostKeyChecking: StrictHostKeyCheckingAcceptNew,
			MaxTimeout:                  0,
			IdleTimeout:                 10 * 60, // 10 minutes
		},
		Git: GitConfig{
			Enabled:        true,
			ListenAddr:     ":9418",
			PublicURL:      "git://localhost",
			MaxTimeout:     0,
			IdleTimeout:    3,
			MaxConnections: 32,
		},
		HTTP: HTTPConfig{
			Enabled:    true,
			ListenAddr: ":23232",
			PublicURL:  "http://localhost:23232",
			CORS: CORSConfig{
				AllowedHeaders: []string{"Accept", "Accept-Language", "Content-Language", "Content-Type", "Origin", "X-Requested-With", "User-Agent", "Authorization", "Access-Control-Request-Method", "Access-Control-Allow-Origin"},
				AllowedMethods: []string{"GET", "HEAD", "POST", "PUT", "OPTIONS"},
				AllowedOrigins: []string{"http://localhost:23232"},
			},
		},
		Stats: StatsConfig{
			Enabled:    true,
			ListenAddr: "localhost:23233",
		},
		Log: LogConfig{
			Format:     "text",
			TimeFormat: time.DateTime,
		},
		DB: DBConfig{
			Driver: "sqlite",
			DataSource: "icy.db" +
				"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)",
		},
		LFS: LFSConfig{
			Enabled:    true,
			SSHEnabled: false,
		},
		Jobs: JobsConfig{
			MirrorPull: "@every 10m",
		},
		Web: WebConfig{
			Enabled: true,
		},
		Webhook: WebhookConfig{
			MaxAttempts:  5,
			RetryBackoff: 2 * time.Minute,
		},
		StateSource: StateSourceDB,
	}
}

// Validate validates the configuration.
// It updates the configuration with absolute paths.
func (c *Config) Validate() error {
	// The data path is handed to ssh inside a double-quoted option value,
	// where nothing escapes a quote or a newline.
	if strings.ContainsAny(c.DataPath, "\"\n\r") {
		return fmt.Errorf("data path must not contain quotes or newlines: %q", c.DataPath)
	}

	// Use absolute paths
	if !filepath.IsAbs(c.DataPath) {
		dp, err := filepath.Abs(c.DataPath)
		if err != nil {
			return err
		}
		c.DataPath = dp
	}

	c.SSH.PublicURL = strings.TrimSuffix(c.SSH.PublicURL, "/")
	c.HTTP.PublicURL = strings.TrimSuffix(c.HTTP.PublicURL, "/")

	if c.SSH.KeyPath != "" && !filepath.IsAbs(c.SSH.KeyPath) {
		c.SSH.KeyPath = filepath.Join(c.DataPath, c.SSH.KeyPath)
	}

	if c.SSH.ClientKeyPath != "" && !filepath.IsAbs(c.SSH.ClientKeyPath) {
		c.SSH.ClientKeyPath = filepath.Join(c.DataPath, c.SSH.ClientKeyPath)
	}

	switch c.SSH.ClientStrictHostKeyChecking {
	case "":
		c.SSH.ClientStrictHostKeyChecking = StrictHostKeyCheckingAcceptNew
	case StrictHostKeyCheckingAcceptNew, StrictHostKeyCheckingYes:
	default:
		// "no" and "off" are not offered: they accept any host key, which
		// hands the client key's authentication to whoever answers.
		return fmt.Errorf("ssh.client_strict_host_key_checking: %q is not one of %q, %q",
			c.SSH.ClientStrictHostKeyChecking, StrictHostKeyCheckingAcceptNew, StrictHostKeyCheckingYes)
	}

	// Drop blanks so an unset env var does not read as one empty entry, then
	// parse once, rejecting anything unreadable now rather than at the first
	// import.
	c.SSH.ClientAllowedHosts = slices.DeleteFunc(slices.Clone(c.SSH.ClientAllowedHosts), func(s string) bool {
		return strings.TrimSpace(s) == ""
	})
	allowed, err := ssrf.ParseAllowedHosts(c.SSH.ClientAllowedHosts)
	if err != nil {
		return fmt.Errorf("ssh.client_allowed_hosts: %w", err)
	}
	c.SSH.allowedHosts = allowed

	if err := c.Webhook.validate(); err != nil {
		return err
	}

	if c.HTTP.TLSKeyPath != "" && !filepath.IsAbs(c.HTTP.TLSKeyPath) {
		c.HTTP.TLSKeyPath = filepath.Join(c.DataPath, c.HTTP.TLSKeyPath)
	}

	if c.HTTP.TLSCertPath != "" && !filepath.IsAbs(c.HTTP.TLSCertPath) {
		c.HTTP.TLSCertPath = filepath.Join(c.DataPath, c.HTTP.TLSCertPath)
	}

	if err := c.validateState(); err != nil {
		return err
	}

	if strings.HasPrefix(c.DB.Driver, "sqlite") && !filepath.IsAbs(c.DB.DataSource) {
		c.DB.DataSource = filepath.Join(c.DataPath, c.DB.DataSource)
	}

	// Validate keys
	pks := make([]string, 0)
	for _, key := range parseAuthKeys(c.InitialAdminKeys) {
		ak := sshutils.MarshalAuthorizedKey(key)
		pks = append(pks, ak)
	}

	c.InitialAdminKeys = pks

	c.HTTP.CORS.AllowedOrigins = append([]string{c.HTTP.PublicURL}, c.HTTP.CORS.AllowedOrigins...)

	return nil
}

// validate parses the webhook allowlist and fills in retry defaults.
func (c *WebhookConfig) validate() error {
	c.AllowedNetworks = slices.DeleteFunc(slices.Clone(c.AllowedNetworks), func(s string) bool {
		return strings.TrimSpace(s) == ""
	})
	nets, err := ssrf.ParseAllowedNetworks(c.AllowedNetworks)
	if err != nil {
		return fmt.Errorf("webhook.allowed_networks: %w", err)
	}
	c.allowedNetworks = nets

	switch {
	case c.MaxAttempts == 0:
		c.MaxAttempts = 5
	case c.MaxAttempts < 0:
		return fmt.Errorf("webhook.max_attempts: must be at least 1, got %d", c.MaxAttempts)
	}
	switch {
	case c.RetryBackoff == 0:
		c.RetryBackoff = 2 * time.Minute
	case c.RetryBackoff < 0:
		return fmt.Errorf("webhook.retry_backoff: must be positive, got %s", c.RetryBackoff)
	}
	return nil
}

// parseAuthKeys parses authorized keys from either file paths or string authorized_keys.
func parseAuthKeys(aks []string) []ssh.PublicKey {
	exist := make(map[string]struct{}, 0)
	pks := make([]ssh.PublicKey, 0)
	for _, key := range aks {
		if bts, err := os.ReadFile(key); err == nil {
			// key is a file
			key = strings.TrimSpace(string(bts))
		}

		if pk, _, err := sshutils.ParseAuthorizedKey(key); err == nil {
			if _, ok := exist[key]; !ok {
				pks = append(pks, pk)
				exist[key] = struct{}{}
			}
		}
	}
	return pks
}

// AdminKeys returns the server admin keys.
func (c *Config) AdminKeys() []ssh.PublicKey {
	return parseAuthKeys(c.InitialAdminKeys)
}

func init() {
	if ex, err := os.Executable(); err == nil {
		binPath = filepath.ToSlash(ex)
	}
}
