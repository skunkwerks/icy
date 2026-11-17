package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skunkwerks/icy/pkg/access"
)

func TestStateSource(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataPath = t.TempDir()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.StateSource != StateSourceDB || cfg.StateFromConfig() {
		t.Errorf("default source = %q", cfg.StateSource)
	}

	if err := cfg.SetStateSource(StateSourceConfig); err != nil || !cfg.StateFromConfig() {
		t.Errorf("SetStateSource(config) = %v, source %q", err, cfg.StateSource)
	}

	// An empty flag leaves the current value alone.
	if err := cfg.SetStateSource(""); err != nil || !cfg.StateFromConfig() {
		t.Errorf("SetStateSource(\"\") changed the source: %q, %v", cfg.StateSource, err)
	}

	if err := cfg.SetStateSource("file"); err == nil {
		t.Error("unknown source accepted")
	}
}

func TestStateSourceFromFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	// The users and repos keys are not part of Config, and the lenient
	// config decoder must keep ignoring them.
	content := "state_source: config\nusers:\n  - name: alice\n    typo_key: 1\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	cfg.DataPath = dir
	if err := cfg.ParseFileFrom(path); err != nil {
		t.Fatal(err)
	}
	if !cfg.StateFromConfig() {
		t.Errorf("state_source from file = %q", cfg.StateSource)
	}

	t.Setenv("ICY_STATE_SOURCE", "db")
	if err := cfg.ParseEnv(); err != nil {
		t.Fatal(err)
	}
	if cfg.StateFromConfig() {
		t.Error("ICY_STATE_SOURCE did not beat config.yaml")
	}

	t.Setenv("ICY_STATE_SOURCE", "nope")
	if err := DefaultConfig().ParseEnv(); err == nil {
		t.Error("bad ICY_STATE_SOURCE accepted")
	}
}

func TestEnvAccessOverrides(t *testing.T) {
	t.Setenv("ICY_ANON_ACCESS", "")
	t.Setenv("ICY_ALLOW_KEYLESS", "")
	anon, allow, err := EnvAccessOverrides()
	if err != nil || anon != nil || allow != nil {
		t.Errorf("empty env = %v, %v, %v", anon, allow, err)
	}

	t.Setenv("ICY_ANON_ACCESS", "no-access")
	t.Setenv("ICY_ALLOW_KEYLESS", "false")
	anon, allow, err = EnvAccessOverrides()
	if err != nil || anon == nil || *anon != access.NoAccess || allow == nil || *allow {
		t.Errorf("set env = %v, %v, %v", anon, allow, err)
	}

	t.Setenv("ICY_ANON_ACCESS", "everyone")
	if _, _, err := EnvAccessOverrides(); err == nil {
		t.Error("bad ICY_ANON_ACCESS accepted")
	}
}
