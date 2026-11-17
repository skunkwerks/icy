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
