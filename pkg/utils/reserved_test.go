package utils

import (
	"errors"
	"testing"
)

func TestValidateNewRepo(t *testing.T) {
	for name, reserved := range map[string]bool{
		"repo":           false,
		"org/repo":       false,
		"logs":           false,
		"changelog":      false,
		"trees/x":        false,
		"my-repo":        false,
		"x-":             false,
		"log":            true,
		"docs/log":       true,
		"org/tree/x":     true,
		"org/tree/tree":  true,
		"Tree":           true,
		"org/COMMIT":     true,
		"-":              true,
		"a/-/b":          true,
		"refs/heads":     true,
		"raw":            true,
		"blob/x":         true,
		"refſ":           true, // the long s folds to s
		"org/refs-cache": false,
	} {
		err := ValidateNewRepo(name)
		if got := errors.Is(err, ErrReservedRepoName); got != reserved {
			t.Errorf("ValidateNewRepo(%q) = %v, want reserved %v", name, err, reserved)
		}
		if HasReservedSegment(name) != reserved {
			t.Errorf("HasReservedSegment(%q) != %v", name, reserved)
		}
		// Existing names with reserved segments still pass ValidateRepo.
		if err := ValidateRepo(name); err != nil {
			t.Errorf("ValidateRepo(%q) = %v", name, err)
		}
	}

	// ValidateRepo's own checks still apply.
	if err := ValidateNewRepo("a/b.git/c"); err == nil || errors.Is(err, ErrReservedRepoName) {
		t.Errorf("ValidateNewRepo(a/b.git/c) = %v", err)
	}
}

func TestReservedSegmentCanonical(t *testing.T) {
	for in, want := range map[string]string{"Log": "log", "TREE": "tree", "-": "-"} {
		got, ok := ReservedSegment(in)
		if !ok || got != want {
			t.Errorf("ReservedSegment(%q) = %q, %v", in, got, ok)
		}
	}
	if _, ok := ReservedSegment("logs"); ok {
		t.Error("logs is reserved")
	}
}
