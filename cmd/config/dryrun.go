package config

import (
	"fmt"

	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
)

// Change describes an edit to the config file, as --dry-run previews it.
//
// The commands that write the config file — auth login, auth logout, config
// use — have no request for out.DryRun to print, and they ignored --dry-run
// altogether: `auth logout --dry-run` deleted the profile. They build a Change
// instead of saving, and PreviewChange prints it.
//
// Never put a token here: a dry run in CI writes this to a log.
type Change struct {
	DryRun bool `json:"dry_run"`
	// Config is the file that would be written.
	Config string `json:"config"`
	// Action is one of save_profile, remove_profile, set_default.
	Action  string `json:"action"`
	Profile string `json:"profile"`
	// Username and Sandbox describe the profile save_profile would write.
	Username string `json:"username,omitempty"`
	Sandbox  *bool  `json:"sandbox,omitempty"`
	// UsesTokenCmd says the profile would get its token from a token_cmd
	// (`auth login --token-cmd`) rather than store one.
	UsesTokenCmd bool `json:"usesTokenCmd,omitempty"`
	// Default is the file's `default:` key after the change; "" for none.
	Default string `json:"default"`

	// Summary is the table-mode sentence, e.g. `remove profile "staging"`.
	Summary string `json:"-"`
}

// PreviewChange prints c in place of writing it: as one document in JSON and
// YAML modes, as one line naming the change and the file otherwise.
func PreviewChange(out *output.Config, c Change) {
	c.DryRun = true
	if c.Config == "" {
		c.Config, _ = config.ActivePath()
	}
	switch out.Format {
	case output.FormatJSON:
		_ = out.JSON(c)
	case output.FormatYAML:
		_ = out.YAML(c)
	default:
		fmt.Fprintf(out.Writer, "%s would %s in %s\n", out.Amber("dry-run:"), c.Summary, c.Config)
	}
}
