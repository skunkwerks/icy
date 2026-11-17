package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/skunkwerks/icy/pkg/utils"
)

// TestCreateRefusesReservedNames checks create and import refuse a name
// with a reserved segment for every caller, before anything that depends
// on whether the name is taken.
func TestCreateRefusesReservedNames(t *testing.T) {
	ctx, be := newAuthTestContext(t)
	callers := map[string]context.Context{
		"anonymous": ctx,
		"user":      withUser(t, ctx, be, "user", false),
		"admin":     withUser(t, ctx, be, "boss", true),
	}
	for who, c := range callers {
		for _, args := range [][]string{
			{"create", "org/tree/x"},
			{"create", "docs/Log"},
			{"create", "-"},
			{"import", "org/refs", "https://example.com/x.git"},
		} {
			if err := runRepo(t, c, args...); !errors.Is(err, utils.ErrReservedRepoName) {
				t.Errorf("%s %v: got %v", who, args, err)
			}
		}
	}
}
