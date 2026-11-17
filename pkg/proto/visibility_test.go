package proto

import "testing"

func TestVisibilityRoundTrip(t *testing.T) {
	for _, v := range Visibilities {
		got, err := ParseVisibility(" " + string(v) + " ")
		if err != nil || got != v {
			t.Errorf("ParseVisibility(%q) = %q, %v", v, got, err)
		}
		if back := VisibilityFromFlags(v.Flags()); back != v {
			t.Errorf("%q round trips to %q", v, back)
		}
	}
	if _, err := ParseVisibility("hidden"); err == nil {
		t.Error("ParseVisibility(hidden) did not fail")
	}
	// Private wins over hidden.
	if v := VisibilityFromFlags(true, true); v != VisibilityPrivate {
		t.Errorf("private and hidden = %q", v)
	}
}

func TestVisibilityListed(t *testing.T) {
	cases := []struct {
		vis      Visibility
		signedIn bool
		want     bool
	}{
		{VisibilityPublic, false, true},
		{VisibilityPublic, true, true},
		{VisibilityUnlisted, false, false},
		{VisibilityUnlisted, true, true},
		// Read access decides who sees a private repository at all.
		// Once a caller can read it, it is listed.
		{VisibilityPrivate, false, true},
		{VisibilityPrivate, true, true},
	}
	for _, c := range cases {
		if got := c.vis.Listed(c.signedIn); got != c.want {
			t.Errorf("%s.Listed(%t) = %t, want %t", c.vis, c.signedIn, got, c.want)
		}
	}
}
