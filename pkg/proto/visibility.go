package proto

import (
	"fmt"
	"strings"
)

// Visibility is who can find and read a repository. It is stored as the
// repository's private and hidden flags, see Flags and VisibilityOf.
type Visibility string

const (
	// VisibilityPublic repositories are listed, and anyone allowed by the
	// server's anon-access setting can read them.
	VisibilityPublic Visibility = "public"
	// VisibilityUnlisted repositories are readable like public ones, but
	// are left out of every listing. Anyone who knows the name can read
	// them.
	VisibilityUnlisted Visibility = "unlisted"
	// VisibilityPrivate repositories are readable only by their owner,
	// collaborators and admins. Everyone else gets the same answer as for
	// a repository that does not exist.
	VisibilityPrivate Visibility = "private"
)

// Visibilities lists the visibility states in order of increasing secrecy.
var Visibilities = []Visibility{VisibilityPublic, VisibilityUnlisted, VisibilityPrivate}

// String implements fmt.Stringer.
func (v Visibility) String() string {
	return string(v)
}

// Flags returns the private and hidden flags that store v.
func (v Visibility) Flags() (private, hidden bool) {
	switch v {
	case VisibilityPrivate:
		return true, false
	case VisibilityUnlisted:
		return false, true
	default:
		return false, false
	}
}

// ParseVisibility parses a visibility name.
func ParseVisibility(s string) (Visibility, error) {
	v := Visibility(strings.ToLower(strings.TrimSpace(s)))
	for _, known := range Visibilities {
		if v == known {
			return v, nil
		}
	}
	return "", fmt.Errorf("invalid visibility %q: want one of public, unlisted, private", s)
}

// VisibilityFromFlags maps the stored flags to a visibility. Private wins
// over hidden: a private repository is private whether or not it is hidden.
func VisibilityFromFlags(private, hidden bool) Visibility {
	switch {
	case private:
		return VisibilityPrivate
	case hidden:
		return VisibilityUnlisted
	default:
		return VisibilityPublic
	}
}

// VisibilityOf returns the visibility of r.
func VisibilityOf(r Repository) Visibility {
	return VisibilityFromFlags(r.IsPrivate(), r.IsHidden())
}
