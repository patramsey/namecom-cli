package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TestComplete_NoFileFallback pins #364: `domain list --tld <TAB>` and
// `order get <TAB>` offered the current directory's files, as did every
// argument and flag value with no completion of its own. It walks the whole
// tree, so a new command or flag is covered without being listed: every
// argument and value flag turns the shell's file fallback off, except the
// flags that take a file, which keep it.
func TestComplete_NoFileFallback(t *testing.T) {
	withConfig(t, loneProfile)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	fileFlags := map[string]bool{
		"api --input": true, "api --field": true,
		"dns import --file": true, "dns sync --file": true,
		"domain contacts set --contacts-file": true, "domain register --contacts-file": true,
		"transfer create --contacts-file": true, "transfer internal-in --contacts-file": true,
	}
	noFiles := func(args ...string) bool {
		d := completeDirective(t, append([]string{"--base-url", srv.URL}, args...)...)
		return d&cobra.ShellCompDirectiveNoFileComp != 0
	}
	seen := 0
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
			return
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
		if c.HasSubCommands() || !c.Runnable() {
			return
		}
		path := strings.Fields(c.CommandPath())[1:]
		name := strings.Join(path, " ")
		if !noFiles(append(path, "")...) {
			t.Errorf("%s <TAB> falls back to file names", name)
		}
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if f.NoOptDefVal != "" || f.Hidden || f.Deprecated != "" {
				return
			}
			seen++
			key := name + " --" + f.Name
			if got := noFiles(append(path, "--"+f.Name, "")...); got == fileFlags[key] {
				if got {
					t.Errorf("%s <TAB> offers no file names; it takes a file", key)
				} else {
					t.Errorf("%s <TAB> falls back to file names", key)
				}
			}
		})
	}
	walk(rootCmd)
	if seen < 50 {
		t.Fatalf("checked %d flags; the walk is not reaching the tree", seen)
	}
	// The root's own flags, which every command inherits.
	if noFiles("status", "--debug-file", "") {
		t.Error("--debug-file <TAB> offers no file names; it takes a file")
	}
	for _, f := range []string{"--timeout", "--token", "--username", "--base-url", "--idempotency-key", "--jq"} {
		if !noFiles("status", f, "") {
			t.Errorf("%s <TAB> falls back to file names", f)
		}
	}
}
