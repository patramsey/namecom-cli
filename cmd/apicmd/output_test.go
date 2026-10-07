package apicmd

import (
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
)

// TestAPI_RejectsOutputItIgnores pins #293 (A16): `namecom api` prints the
// response body as received, so -o yaml, -o tsv and -q did nothing, and it
// said nothing either: raw JSON, exit 0. They are usage errors now, before
// any request. JSON and table mode stay, since they pick the error format.
func TestAPI_RejectsOutputItIgnores(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		set        func(*output.Config)
	}{
		{"-o yaml", "-o yaml", func(o *output.Config) { o.Format = output.FormatYAML }},
		{"-o tsv", "-o tsv", func(o *output.Config) { o.Format = output.FormatTSV }},
		{"-q", "--quiet", func(o *output.Config) { o.QuietMode = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, _ := apiCmd(t, refuseAll(t))
			tc.set(cmdutil.Out(cmd))
			err := runAPI(cmd, []string{"GET", "/core/v1/hello"})
			if !isUsage(err) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want a usage error naming %s", err, tc.want)
			}
		})
	}

	for _, f := range []output.Format{output.FormatJSON, output.FormatTable} {
		srv, got := recordServer(t, `{}`)
		cmd, _ := apiCmd(t, srv)
		cmdutil.Out(cmd).Format = f
		if err := runAPI(cmd, []string{"GET", "/core/v1/hello"}); err != nil {
			t.Errorf("-o %s: %v", f, err)
		}
		if len(*got) != 1 {
			t.Errorf("-o %s: sent %d requests, want 1", f, len(*got))
		}
	}
}
