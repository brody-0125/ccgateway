package proxy

import "testing"

func TestNormalizeReleaseTag(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "", want: ""},
		{in: "latest", want: "latest"},
		{in: "LATEST", want: "latest"},
		{in: "v6.8.15", want: "v6.8.15"},
		{in: "V6.8.15", want: "V6.8.15"},
		{in: "6.8.15", want: "v6.8.15"},
		{in: " 6.8.15 ", want: "v6.8.15"},
		{in: "release-foo", want: "release-foo"},
	}

	for _, tc := range tests {
		got := normalizeReleaseTag(tc.in)
		if got != tc.want {
			t.Fatalf("normalizeReleaseTag(%q)=%q, want %q", tc.in, got, tc.want)
		}
	}
}
