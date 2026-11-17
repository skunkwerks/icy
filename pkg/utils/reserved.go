package utils

import (
	"errors"
	"fmt"
	"strings"
)

// ReservedRepoSegments are the path segments no new repository name may
// contain. The web UI uses them to split a URL into a repository name and a
// page: the first reserved segment ends the name. Because no repository can
// contain one, that split never depends on which repositories exist.
//
// "-" holds the server's own pages, such as /-/login. The rest are the
// repository pages. Every word added here can break a name that is valid
// today, so keep the list short.
var ReservedRepoSegments = []string{"-", "blob", "commit", "log", "raw", "refs", "tree"}

// ErrReservedRepoName is returned for a new repository name that has a
// reserved segment.
var ErrReservedRepoName = errors.New("repo name has a reserved segment")

// ReservedSegment reports whether seg is reserved and returns its canonical
// spelling. Case is ignored: a name that differs only in case shares a
// directory with the reserved spelling on a case-insensitive filesystem,
// and the same URL segment should mean the same thing however it is typed.
func ReservedSegment(seg string) (string, bool) {
	for _, r := range ReservedRepoSegments {
		if strings.EqualFold(seg, r) {
			return r, true
		}
	}
	return "", false
}

// HasReservedSegment reports whether any segment of a repository name is
// reserved.
func HasReservedSegment(repo string) bool {
	for _, seg := range strings.Split(repo, "/") {
		if _, ok := ReservedSegment(seg); ok {
			return true
		}
	}
	return false
}

// ValidateNewRepo checks a name chosen for a new repository: everything
// ValidateRepo checks, and no reserved segment. Use it where a name is
// created (create, import, the new name of a rename, push-to-create, the
// default repository, declarative config). Lookups of existing
// repositories keep using ValidateRepo, so a repository created before a
// segment was reserved keeps working.
func ValidateNewRepo(repo string) error {
	if err := ValidateRepo(repo); err != nil {
		return err
	}
	for _, seg := range strings.Split(repo, "/") {
		if _, ok := ReservedSegment(seg); ok {
			return fmt.Errorf("%w: %q is used by the web UI (reserved: %s)",
				ErrReservedRepoName, seg, strings.Join(ReservedRepoSegments, ", "))
		}
	}
	return nil
}
