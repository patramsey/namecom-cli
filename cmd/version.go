package cmd

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version and build information",
	RunE:  runVersion,
}

type buildInfo struct {
	Version string `json:"version"         yaml:"version"`
	Commit  string `json:"commit,omitempty" yaml:"commit,omitempty"`
	Dirty   bool   `json:"dirty"            yaml:"dirty"`
	Built   string `json:"built,omitempty"  yaml:"built,omitempty"`
	Go      string `json:"go"               yaml:"go"`
	OS      string `json:"os"               yaml:"os"`
	Arch    string `json:"arch"             yaml:"arch"`
}

func runVersion(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)
	info := gatherBuildInfo()

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(info)
	case output.FormatYAML:
		return out.YAML(info)
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
		built := info.Built
		if built == "" {
			built = "unknown"
		}
		fmt.Fprintf(out.Writer, "namecom %s\n", info.Version)
		fmt.Fprintf(out.Writer, "  commit: %s%s\n", commit, suffix)
		fmt.Fprintf(out.Writer, "  built:  %s\n", built)
		fmt.Fprintf(out.Writer, "  go:     %s\n", info.Go)
		fmt.Fprintf(out.Writer, "  os:     %s/%s\n", info.OS, info.Arch)
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
			info.Built = s.Value
		case "vcs.modified":
			info.Dirty = s.Value == "true"
		}
	}
	return info
}
