package update

import "testing"

// TestIsNewer covers the comparison behind the upgrade notice.
//
// It was the only piece of that path with no test, and it had a bug: the
// function prepends "v" unconditionally, so a version string that already
// carries one becomes "vv0.4.0", fails semver validation, and reports false.
//
// That is not hypothetical. The two build paths disagree about the prefix —
// goreleaser sets `main.version` from `{{.Version}}` ("0.4.0") while the
// Makefile sets it from `git describe` ("v0.4.0-2-g48cf186") — so the notice
// silently never fired for anyone running `make install` or a local build.
// Nothing surfaced it, because the failure is a message that does not appear.
func TestIsNewer(t *testing.T) {
	tests := []struct {
		name               string
		candidate, current string
		want               bool
	}{
		{"newer patch", "0.4.1", "0.4.0", true},
		{"same version", "0.4.0", "0.4.0", false},
		{"older candidate", "0.3.2", "0.4.0", false},

		// Ordering that a string comparison gets wrong.
		{"10 is newer than 9", "0.10.0", "0.9.0", true},
		{"9 is not newer than 10", "0.9.0", "0.10.0", false},

		// Either side may arrive with a "v", depending on how the binary was
		// built. Both must be handled.
		{"v-prefixed current", "0.4.1", "v0.4.0", true},
		{"v-prefixed candidate", "v0.4.1", "0.4.0", true},
		{"both v-prefixed", "v0.4.1", "v0.4.0", true},

		// `git describe` appends commits-since-tag. Such a build is not the
		// release it is derived from, and semver orders a pre-release below it.
		{"untagged build is older than its tag", "0.4.0", "v0.4.0-2-g48cf186", true},

		// Garbage must not be reported as an upgrade.
		{"unparseable candidate", "not-a-version", "0.4.0", false},
		{"unparseable current", "0.4.1", "not-a-version", false},
		{"empty candidate", "", "0.4.0", false},
		{"empty current", "0.4.1", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNewer(tc.candidate, tc.current); got != tc.want {
				t.Errorf("isNewer(%q, %q) = %v, want %v", tc.candidate, tc.current, got, tc.want)
			}
		})
	}
}

// TestCheck_SkipsUntaggedBuilds guards the consequence of teaching isNewer to
// handle a "v" prefix: `git describe` output like "v0.4.0-2-g48cf186" is
// semver-older than "v0.4.0", even though in git terms it is ahead of it.
//
// Without this, fixing the prefix handling would have started telling anyone
// who built from source to "upgrade" to the release they had just built past —
// turning a notice that never fired into one that fires and is wrong.
func TestCheck_SkipsUntaggedBuilds(t *testing.T) {
	for _, current := range []string{
		"v0.4.0-2-g48cf186", // commits since the tag
		"0.4.0-2-g48cf186",  // same, without the prefix
		"v0.4.0-dirty",      // uncommitted changes
	} {
		t.Run(current, func(t *testing.T) {
			// No HTTP stub: Check must return before it would reach the
			// network, so a request here would hang or fail the run.
			if msg := Check(current); msg != "" {
				t.Errorf("Check(%q) = %q, want no notice for an untagged build", current, msg)
			}
		})
	}
}
