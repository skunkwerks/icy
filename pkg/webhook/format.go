package webhook

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/skunkwerks/icy/git"
)

// Format is the shape of a webhook's request body.
type Format string

const (
	// FormatIcy is Icy's own payload, with the event, repository, sender
	// and event fields.
	FormatIcy Format = "icy"
	// FormatBuildkite is the body of a Buildkite "create a build" request.
	// It is sent for pushes to branches only.
	FormatBuildkite Format = "buildkite"
)

var (
	// ErrInvalidFormat is returned for an unknown payload format.
	ErrInvalidFormat = errors.New("invalid format, can be either `icy` or `buildkite`")
	// ErrFormatEvents is returned when a buildkite webhook subscribes to
	// anything but the push event.
	ErrFormatEvents = errors.New("the buildkite format works only with the push event")
	// ErrFormatContentType is returned when a buildkite webhook asks for
	// a form body. Buildkite takes JSON only.
	ErrFormatContentType = errors.New("the buildkite format is always sent as json")
)

// ParseFormat parses a payload format name.
func ParseFormat(s string) (Format, error) {
	switch f := Format(strings.ToLower(strings.TrimSpace(s))); f {
	case FormatIcy, FormatBuildkite:
		return f, nil
	default:
		return "", ErrInvalidFormat
	}
}

// FormatOf returns the format stored for a webhook. Rows written before
// formats existed have the icy format.
func FormatOf(s string) Format {
	if s == "" {
		return FormatIcy
	}
	return Format(s)
}

// String returns the format name.
func (f Format) String() string {
	return string(f)
}

// ValidateFormat checks that a webhook's format fits its events and
// content type.
func ValidateFormat(f Format, events []Event, contentType ContentType) error {
	switch f {
	case FormatIcy:
		return nil
	case FormatBuildkite:
		if len(events) == 0 {
			return ErrFormatEvents
		}
		for _, e := range events {
			if e != EventPush {
				return ErrFormatEvents
			}
		}
		if contentType != ContentTypeJSON {
			return ErrFormatContentType
		}
		return nil
	default:
		return ErrInvalidFormat
	}
}

// buildkiteBuild is the body of a Buildkite create build request.
type buildkiteBuild struct {
	Commit   string            `json:"commit"`
	Branch   string            `json:"branch"`
	Message  string            `json:"message"`
	Author   buildkiteAuthor   `json:"author"`
	MetaData buildkiteMetaData `json:"meta_data"`
}

type buildkiteAuthor struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type buildkiteMetaData struct {
	Delivery string `json:"icy_delivery"`
	Repo     string `json:"icy_repo"`
	// Tag is set for tag pushes, so a pipeline can tell them apart with
	// `buildkite-agent meta-data get icy_tag`.
	Tag string `json:"icy_tag,omitempty"`
}

// buildkiteBody renders a push as a Buildkite create build request. A
// branch push builds that branch. A tag push builds the tagged commit with
// the tag name as the branch, which is how the Buildkite API builds a tag.
// It reports false for pushes that must not start a build: a deleted
// branch or tag, and any ref that is neither a branch nor a tag.
func buildkiteBody(p PushEvent, delivery uuid.UUID) (string, bool, error) {
	if p.After == "" || git.IsZeroHash(p.After) {
		return "", false, nil
	}
	var branch, tag string
	if b, ok := strings.CutPrefix(p.Ref, "refs/heads/"); ok {
		branch = b
	} else if t, ok := strings.CutPrefix(p.Ref, "refs/tags/"); ok {
		branch, tag = t, t
	}
	if branch == "" {
		return "", false, nil
	}

	build := buildkiteBuild{
		Commit: p.After,
		Branch: branch,
		MetaData: buildkiteMetaData{
			Delivery: delivery.String(),
			Repo:     p.Repository.Name,
			Tag:      tag,
		},
	}
	// Commits are listed newest first. For an annotated tag, After names
	// the tag object, and the newest commit is the commit it points at,
	// which is what Buildkite has to check out.
	if len(p.Commits) > 0 {
		c := p.Commits[0]
		if tag != "" && c.ID != "" {
			build.Commit = c.ID
		}
		build.Message = c.Title
		build.Author = buildkiteAuthor{Name: c.Author.Name, Email: c.Author.Email}
	}

	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(build); err != nil {
		return "", false, err //nolint:wrapcheck
	}
	return b.String(), true, nil
}
