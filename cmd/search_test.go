package cmd

import (
	"fmt"
	"testing"
)

func TestResolveWorkType(t *testing.T) {
	cases := []struct {
		name                                          string
		remote, hybrid, onsite                        bool
		want                                          string
	}{
		{"none", false, false, false, ""},
		{"remote_only", true, false, false, "2"},
		{"hybrid_only", false, true, false, "3"},
		{"onsite_only", false, false, true, "1"},
		{"remote_hybrid", true, true, false, "2,3"},
		{"remote_onsite", true, false, true, "1,2"},
		{"hybrid_onsite", false, true, true, "1,3"},
		{"all_three", true, true, true, "1,2,3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveWorkType(c.remote, c.hybrid, c.onsite)
			if got != c.want {
				t.Errorf("resolveWorkType(remote=%v, hybrid=%v, onsite=%v) = %q, want %q",
					c.remote, c.hybrid, c.onsite, got, c.want)
			}
		})
	}
}

func TestWorkTypeLabel(t *testing.T) {
	cases := []struct {
		name                                   string
		remote, hybrid, onsite                 bool
		want                                   string
	}{
		{"none", false, false, false, ""},
		{"remote_only", true, false, false, "remote"},
		{"hybrid_only", false, true, false, "hybrid"},
		{"onsite_only", false, false, true, "onsite"},
		{"remote_hybrid", true, true, false, "remote/hybrid"},
		{"all_three", true, true, true, "onsite/remote/hybrid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := workTypeLabel(c.remote, c.hybrid, c.onsite)
			if got != c.want {
				t.Errorf("workTypeLabel(remote=%v, hybrid=%v, onsite=%v) = %q, want %q",
					c.remote, c.hybrid, c.onsite, got, c.want)
			}
		})
	}
}

func TestResolvePostedWithin(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "", false},
		{"   ", "", false},
		{"1d", "r86400", false},
		{"7d", "r604800", false},
		{"30d", "r2592000", false},
		{"365d", "r31536000", false},
		{"0d", "", true},     // non-positive
		{"-3d", "", true},    // negative
		{"7", "", true},      // missing 'd' suffix
		{"7days", "", true},  // extra chars after number
		{"week", "", true},   // not Nd
		{"24h", "", true},    // hours not accepted
		{"d", "", true},      // no digits
		{"1.5d", "", true},   // non-integer
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%q", c.in), func(t *testing.T) {
			got, err := resolvePostedWithin(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("resolvePostedWithin(%q) = %q, nil; want error", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolvePostedWithin(%q) = _, %v; want %q, nil", c.in, err, c.want)
			}
			if got != c.want {
				t.Errorf("resolvePostedWithin(%q) = %q, nil; want %q", c.in, got, c.want)
			}
		})
	}
}
