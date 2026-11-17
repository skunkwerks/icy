package serve

import (
	"testing"

	"github.com/skunkwerks/icy/pkg/access"
	"github.com/skunkwerks/icy/pkg/config"
)

func TestSameServerSettings(t *testing.T) {
	dir := t.TempDir()
	parsed := func() *config.Config {
		cfg := config.DefaultConfig()
		cfg.DataPath = dir
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		return cfg
	}

	running := parsed()
	// The running config ran Validate twice, as it does when both a file
	// and the environment are parsed.
	if err := running.Validate(); err != nil {
		t.Fatal(err)
	}
	running.StateSource = config.StateSourceConfig

	fresh := parsed()
	level := access.NoAccess
	fresh.AnonAccess = &level
	if !sameServerSettings(running, fresh) {
		t.Error("a change to anon_access or state_source counted as a server change")
	}

	fresh.SSH.ListenAddr = ":2222"
	if sameServerSettings(running, fresh) {
		t.Error("a new ssh listen address went unnoticed")
	}
}
