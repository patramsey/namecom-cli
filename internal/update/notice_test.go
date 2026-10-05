package update

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestUpgradeHint guards #239: the notice said only "see
// github.com/…/releases". It now names the command for how this copy was
// installed.
func TestUpgradeHint(t *testing.T) {
	home := t.TempDir()
	gopath := filepath.Join(home, "gopath")
	gobin := filepath.Join(home, "gobin")
	t.Setenv("GOPATH", gopath)
	t.Setenv("GOBIN", gobin)

	for _, tt := range []struct {
		name, exe, want string
	}{
		{"homebrew on Apple silicon", "/opt/homebrew/Cellar/namecom/0.4.9/bin/namecom", "brew upgrade namecom"},
		{"homebrew on Linux", "/home/linuxbrew/.linuxbrew/Cellar/namecom/0.4.9/bin/namecom", "brew upgrade namecom"},
		{"go install into GOPATH/bin", filepath.Join(gopath, "bin", "namecom"), "go install github.com/patramsey/namecom-cli@latest"},
		{"go install into GOBIN", filepath.Join(gobin, "namecom"), "go install github.com/patramsey/namecom-cli@latest"},
		{"go install's own name", filepath.Join(home, "elsewhere", "namecom-cli"), "go install github.com/patramsey/namecom-cli@latest"},
		{"downloaded archive", filepath.Join(home, "bin", "namecom"), "releases/latest"},
		{"unknown path", "", "releases/latest"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := upgradeHint(tt.exe); !strings.Contains(got, tt.want) {
				t.Errorf("upgradeHint(%q) = %q, want it to contain %q", tt.exe, got, tt.want)
			}
		})
	}
}

func TestNotice(t *testing.T) {
	got := notice("0.5.0", "v0.4.9", "To upgrade, run: brew upgrade namecom")
	for _, want := range []string{"v0.4.9 → v0.5.0", "brew upgrade namecom", "NAMECOM_NO_UPDATE_NOTIFIER=1"} {
		if !strings.Contains(got, want) {
			t.Errorf("notice does not contain %q:\n%s", want, got)
		}
	}
}

// TestDisabled: NAMECOM_NO_UPDATE_NOTIFIER turns the notice off, and Check
// then returns before reading the cache or the network.
func TestDisabled(t *testing.T) {
	for v, want := range map[string]bool{
		"": false, "0": false, "false": false, "off": false,
		"1": true, "true": true, "yes": true,
	} {
		t.Setenv(DisableEnv, v)
		if got := Disabled(); got != want {
			t.Errorf("%s=%q: Disabled() = %v, want %v", DisableEnv, v, got, want)
		}
	}

	isolateConfigDir(t)
	writeCache("99.0.0")
	t.Setenv(DisableEnv, "1")
	if got := Check("0.1.0"); got != "" {
		t.Errorf("Check with %s=1 = %q, want nothing", DisableEnv, got)
	}
}
