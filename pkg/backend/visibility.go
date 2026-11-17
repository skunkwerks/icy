package backend

import (
	"context"

	"github.com/skunkwerks/icy/pkg/proto"
)

// SetVisibility sets a repository's visibility through its private and
// hidden flags.
//
// The two flags are written one at a time, in the order that never leaves
// the repository more exposed than both the old and the new state. Making a
// repository private sets the private flag first. Opening one up clears
// the private flag last, after the hidden flag already says whether it is
// listed.
func (d *Backend) SetVisibility(ctx context.Context, name string, v proto.Visibility) error {
	private, hidden := v.Flags()
	if private {
		if err := d.SetPrivate(ctx, name, true); err != nil {
			return err
		}
		return d.SetHidden(ctx, name, hidden)
	}

	if err := d.SetHidden(ctx, name, hidden); err != nil {
		return err
	}
	return d.SetPrivate(ctx, name, false)
}
