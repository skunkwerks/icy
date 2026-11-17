package serve

import (
	"context"
	"fmt"
	"reflect"
	"slices"

	"charm.land/log/v2"
	"github.com/skunkwerks/icy/pkg/backend"
	"github.com/skunkwerks/icy/pkg/config"
	"github.com/skunkwerks/icy/pkg/statefile"
)

// applyConfigState makes the database match the users, repos and access
// settings in config.yaml when state_source is "config". Invalid state
// stops the server from starting. In db mode it only warns if config.yaml
// lists users or repos, since they are ignored.
func applyConfigState(ctx context.Context) error {
	cfg := config.FromContext(ctx)
	logger := log.FromContext(ctx).WithPrefix("state")

	if !cfg.StateFromConfig() {
		warnIgnoredState(cfg, logger)
		return nil
	}

	changes, err := backend.FromContext(ctx).ApplyConfigState(ctx)
	if err != nil {
		return fmt.Errorf("state_source is config: %w", err)
	}
	logChanges(logger, cfg.ConfigPath(), changes)
	return nil
}

// warnIgnoredState warns when config.yaml has users or repos keys that db
// mode does not read. It never fails: the keys are harmless there.
func warnIgnoredState(cfg *config.Config, logger *log.Logger) {
	if !cfg.Exist() {
		return
	}
	f, err := statefile.ParseFile(cfg.ConfigPath())
	if err != nil || !f.HasState() {
		return
	}
	logger.Warn(`config.yaml has users or repos keys, which are ignored unless state_source is "config"`,
		"file", cfg.ConfigPath())
}

// ReloadState re-reads config.yaml and applies its users, repos and access
// settings. Other settings need a restart, and a change to them is logged.
// Invalid state is logged and the server keeps its current state.
func (s *Server) ReloadState() {
	if !s.Config.StateFromConfig() {
		return
	}

	logger := s.logger.WithPrefix("state")
	path := s.Config.ConfigPath()

	fresh := config.DefaultConfig()
	if err := fresh.ParseFileFrom(path); err != nil {
		logger.Error("config.yaml not applied, keeping the current state", "err", err)
		return
	}
	if err := fresh.ParseEnv(); err != nil {
		logger.Error("config.yaml not applied, keeping the current state", "err", err)
		return
	}
	if !sameServerSettings(s.Config, fresh) {
		logger.Warn("config.yaml has changes outside users, repos, anon_access and allow_keyless, which need a restart to take effect")
	}

	changes, err := s.Backend.ApplyConfigState(s.ctx)
	if err != nil {
		logger.Error("config.yaml not applied, keeping the current state", "err", err)
		return
	}
	logChanges(logger, path, changes)
}

// sameServerSettings reports whether two configs agree on everything but
// the settings a reload applies. state_source needs a restart too, but the
// running value may come from a flag, so it is left out.
func sameServerSettings(running, fresh *config.Config) bool {
	a, b := *running, *fresh
	for _, c := range []*config.Config{&a, &b} {
		c.AnonAccess, c.AllowKeyless, c.StateSource = nil, nil, ""
		// Validate prepends the public URL to the allowed origins each
		// time it runs, and the running config may have run it more often.
		c.HTTP.CORS.AllowedOrigins = dedupe(c.HTTP.CORS.AllowedOrigins)
	}
	return reflect.DeepEqual(a, b)
}

func dedupe(s []string) []string {
	out := make([]string, 0, len(s))
	for _, v := range s {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func logChanges(logger *log.Logger, path string, changes []string) {
	for _, c := range changes {
		logger.Info(c)
	}
	logger.Info("users, repos and access applied", "file", path, "changes", len(changes))
}
