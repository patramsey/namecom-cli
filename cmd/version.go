package cmd

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version and build information",
	Args:  cobra.NoArgs,
	RunE:  runVersion,
}

// buildInfo is what `namecom version` reports. CommitTime is the time of the
// commit the binary was built from (vcs.time), not when it was built; it was
// labelled "built" until #187. No build timestamp is recorded.
type buildInfo struct {
	Version    string `json:"version"              yaml:"version"`
	Commit     string `json:"commit,omitempty"     yaml:"commit,omitempty"`
	Dirty      bool   `json:"dirty"                yaml:"dirty"`
	CommitTime string `json:"commitTime,omitempty" yaml:"commitTime,omitempty"`
	Go         string `json:"go"                   yaml:"go"`
	OS         string `json:"os"                   yaml:"os"`
	Arch       string `json:"arch"                 yaml:"arch"`
}

func runVersion(cmd *cobra.Command, _ []string) error {
	return renderVersion(cmdutil.Out(cmd), gatherBuildInfo())
}

func renderVersion(out *output.Config, info buildInfo) error {
	// Quiet prints the bare version string.
	if out.Quiet(info.Version) {
		return nil
	}
	switch out.Format {
	case output.FormatJSON:
		return out.JSON(info)
	case output.FormatYAML:
		return out.YAML(info)
	case output.FormatTSV:
		return out.TSVObject(info)
	default:
		commit := info.Commit
		if commit == "" {
			commit = "unknown"
		} else if len(commit) > 7 {
			commit = commit[:7]
		}
		suffix := " (clean)"
		if info.Dirty {
			suffix = " (dirty)"
		}
		committed := info.CommitTime
		if committed == "" {
			committed = "unknown"
		}
		fmt.Fprintf(out.Writer, "namecom %s\n", info.Version)
		fmt.Fprintf(out.Writer, "  commit:    %s%s\n", commit, suffix)
		fmt.Fprintf(out.Writer, "  committed: %s\n", committed)
		fmt.Fprintf(out.Writer, "  go:        %s\n", info.Go)
		fmt.Fprintf(out.Writer, "  os:        %s/%s\n", info.OS, info.Arch)
	}
	return nil
}

func gatherBuildInfo() buildInfo {
	info := buildInfo{
		Version: Version,
		Go:      runtime.Version(),
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			info.Commit = s.Value
		case "vcs.time":
			info.CommitTime = s.Value
		case "vcs.modified":
			info.Dirty = s.Value == "true"
		}
	}
	return info
}

// resolveVersion returns Version when set by ldflags (release builds), and
// falls back to the module version the toolchain embeds: the tag for
// `go install …@v0.1.7`, and since Go 1.24 a pseudo-version derived from git
// for a plain `go build` in a checkout (e.g. 0.1.8-0.20260901120000-abcdef123456,
// with +dirty for uncommitted changes). "(devel)" now appears only without VCS
// information, such as `go run` or a source tarball; that stays "dev".
func resolveVersion() string {
	if Version != "dev" {
		return Version
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return Version
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return strings.TrimPrefix(v, "v")
	}
	return Version
}
