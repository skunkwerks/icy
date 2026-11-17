package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/skunkwerks/icy/pkg/access"
)

// validateState checks the state source.
func (c *Config) validateState() error {
	switch c.StateSource {
	case "":
		c.StateSource = StateSourceDB
	case StateSourceDB, StateSourceConfig:
	default:
		return fmt.Errorf("state_source: %q is not one of %q, %q",
			c.StateSource, StateSourceDB, StateSourceConfig)
	}
	return nil
}

// SetStateSource overrides the state source, for a command line flag. An
// empty value leaves the current setting alone.
func (c *Config) SetStateSource(source string) error {
	if source != "" {
		c.StateSource = source
	}
	return c.validateState()
}

// StateFromConfig reports whether users, repositories and access settings
// come from config.yaml rather than the database.
func (c *Config) StateFromConfig() bool {
	return c != nil && c.StateSource == StateSourceConfig
}

// ParseFileFrom parses and validates the config file at path instead of
// the default one.
func (c *Config) ParseFileFrom(path string) error {
	return parseFile(c, path)
}

// EnvAccessOverrides returns the values of ICY_ANON_ACCESS and
// ICY_ALLOW_KEYLESS, or nil for each one that is unset or empty. Unlike
// Config.AnonAccess and Config.AllowKeyless it leaves out config.yaml, so
// that a reload can tell the two apart.
func EnvAccessOverrides() (*access.AccessLevel, *bool, error) {
	var anon *access.AccessLevel
	var allow *bool

	if v := os.Getenv("ICY_ANON_ACCESS"); v != "" {
		l := access.ParseAccessLevel(v)
		if l < 0 {
			return nil, nil, fmt.Errorf("ICY_ANON_ACCESS: %w: %q", access.ErrInvalidAccessLevel, v)
		}
		anon = &l
	}

	if v := os.Getenv("ICY_ALLOW_KEYLESS"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, nil, fmt.Errorf("ICY_ALLOW_KEYLESS: %w", err)
		}
		allow = &b
	}

	return anon, allow, nil
}
