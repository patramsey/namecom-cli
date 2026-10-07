package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// TestOpen_DryRunLaunchesNothing pins #292: `open --dry-run` launched the
// browser and reported "opened": true. It prints the URL and starts nothing.
func TestOpen_DryRunLaunchesNothing(t *testing.T) {
	const target = "https://www.name.com/account/domain/details#?domain=acme.io"
	for _, format := range []output.Format{output.FormatJSON, output.FormatTable} {
		t.Run(string(format), func(t *testing.T) {
			calls := stubStart(t, func(name string) error {
				t.Errorf("--dry-run started %s", name)
				return nil
			})
			out, stdout, stderr := openOut(format)
			root := &cobra.Command{Use: "namecom"}
			var dr bool
			root.PersistentFlags().BoolVar(&dr, "dry-run", false, "")
			if err := root.PersistentFlags().Set("dry-run", "true"); err != nil {
				t.Fatal(err)
			}
			child := &cobra.Command{Use: "open"}
			root.AddCommand(child)
			child.SetContext(context.WithValue(context.Background(), cmdutil.KeyOutput, out))

			if err := runOpen(child, []string{"acme.io"}); err != nil {
				t.Fatalf("runOpen: %v", err)
			}
			if len(*calls) != 0 {
				t.Errorf("calls = %q, want none", *calls)
			}
			if format == output.FormatJSON {
				var got map[string]any
				if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
					t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
				}
				if got["url"] != target || got["opened"] != false || got["dryRun"] != true {
					t.Errorf("got %v", got)
				}
				return
			}
			if !strings.Contains(stdout.String(), target) || strings.Contains(stderr.String(), "Opening") {
				t.Errorf("stdout = %q, stderr = %q", stdout, stderr)
			}
		})
	}
}
