package core

import "testing"

// A name that starts with two dots is inside its folder; only ".." climbs.
func TestIsInsideDotNames(t *testing.T) {
	for path, want := range map[string]bool{
		"/r/...": true, "/r/..env": true, "/r/a/../b": true, "/r": true,
		"/r/../x": false, "/x": false, "/rr/a": false,
	} {
		if got := IsInside(path, "/r"); got != want {
			t.Errorf("IsInside(%s, /r) = %v, want %v", path, got, want)
		}
	}
}
