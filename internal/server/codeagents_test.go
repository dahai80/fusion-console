package server

import "testing"

func TestVersionLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.1.120", "0.1.121", true},
		{"0.1.121", "0.1.121", false},
		{"0.1.122", "0.1.121", false},
		{"v0.1.9", "0.1.10", true},
		{"2.1.325", "2.1.325", false},
		{"1.0.0-beta", "1.0.0", false}, // build metadata stripped → equal
	}
	for _, c := range cases {
		if got := versionLess(c.a, c.b); got != c.want {
			t.Errorf("versionLess(%q,%q) = %v want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("\n  0.1.121 (AtlasCode) \nnext"); got != "0.1.121 (AtlasCode)" {
		t.Errorf("firstLine = %q", got)
	}
}
