package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/internal/output"
)

func TestGatherBuildInfo_Version(t *testing.T) {
	Version = "1.2.3"
	t.Cleanup(func() { Version = "dev" })

	info := gatherBuildInfo()
	if info.Version != "1.2.3" {
		t.Errorf("Version = %q, want %q", info.Version, "1.2.3")
	}
	if info.Go == "" {
		t.Error("Go field is empty")
	}
	if info.OS == "" {
		t.Error("OS field is empty")
	}
	if info.Arch == "" {
		t.Error("Arch field is empty")
	}
}

func TestRunVersion_TableOutput(t *testing.T) {
	tests := []struct {
		name    string
		info    buildInfo
		contain []string
	}{
		{
			name: "full info",
			info: buildInfo{
				Version: "1.2.3",
				Commit:  "abc1234567890",
				Dirty:   false,
				Built:   "2025-01-01T00:00:00Z",
				Go:      "go1.26.4",
				OS:      "linux",
				Arch:    "amd64",
			},
			contain: []string{"1.2.3", "abc1234", "(clean)", "2025-01-01", "go1.26.4", "linux/amd64"},
		},
		{
			name:    "dirty build",
			info:    buildInfo{Version: "0.1.0", Commit: "deadbeef", Dirty: true, Go: "go1.26.4", OS: "darwin", Arch: "arm64"},
			contain: []string{"(dirty)", "deadbee"},
		},
		{
			name:    "no vcs info",
			info:    buildInfo{Version: "dev", Go: "go1.26.4", OS: "darwin", Arch: "arm64"},
			contain: []string{"unknown", "(clean)"},
		},
		{
			name:    "short commit not truncated",
			info:    buildInfo{Version: "1.0.0", Commit: "abc123", Go: "go1.26.4", OS: "linux", Arch: "amd64"},
			contain: []string{"abc123"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			out := &output.Config{Format: output.FormatTable, Writer: &buf}
			if err := renderVersion(out, tt.info); err != nil {
				t.Fatalf("renderVersion: %v", err)
			}
			got := buf.String()
			for _, s := range tt.contain {
				if !strings.Contains(got, s) {
					t.Errorf("output missing %q:\n%s", s, got)
				}
			}
		})
	}
}

func TestRunVersion_JSONOutput(t *testing.T) {
	var buf bytes.Buffer
	out := &output.Config{Format: output.FormatJSON, Writer: &buf}
	info := buildInfo{
		Version: "1.2.3",
		Commit:  "abc123",
		Dirty:   true,
		Built:   "2025-01-01T00:00:00Z",
		Go:      "go1.26.4",
		OS:      "linux",
		Arch:    "amd64",
	}
	if err := renderVersion(out, info); err != nil {
		t.Fatalf("renderVersion: %v", err)
	}

	var got buildInfo
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Version != info.Version {
		t.Errorf("Version = %q, want %q", got.Version, info.Version)
	}
	if got.Commit != info.Commit {
		t.Errorf("Commit = %q, want %q", got.Commit, info.Commit)
	}
	if !got.Dirty {
		t.Error("Dirty = false, want true")
	}
}
