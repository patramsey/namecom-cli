package apicmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
)

// TestAPI_WritesNeedYesOffATerminal pins #282: `namecom api` sent POST, PUT,
// PATCH and DELETE with no confirmation, even off a terminal, where every
// other write stops with confirmation_required until --yes is passed. Adding
// -f perPage=2 to the help's first example made it an unconfirmed POST to the
// registration endpoint.
func TestAPI_WritesNeedYesOffATerminal(t *testing.T) {
	defer output.StubInteractive(false)()
	for _, tc := range []struct {
		name string
		args []string
		set  func()
	}{
		{"-f infers a POST", []string{"/core/v1/domains"}, func() { apiFields = []string{"perPage=2"} }},
		{"explicit POST", []string{"POST", "/core/v1/domains"}, func() { apiBody = `{}` }},
		{"DELETE", []string{"DELETE", "/core/v1/domains/example.com/records/1"}, func() {}},
		{"-X PATCH", []string{"/core/v1/domains/example.com"}, func() { apiMethod = "patch"; apiTyped = []string{"locked=false"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, _ := apiCmd(t, refuseAll(t))
			withRoot(cmd, false, false)
			tc.set()
			err := runAPI(cmd, tc.args)
			if _, ok := errors.AsType[*cmdutil.ConfirmationRequiredError](err); !ok || !isUsage(err) {
				t.Fatalf("err = %v, want a confirmation_required usage error", err)
			}
		})
	}

	t.Run("--yes sends it once", func(t *testing.T) {
		srv, got := recordServer(t, `{}`)
		cmd, _ := apiCmd(t, srv)
		apiFields = []string{"perPage=2"}
		if err := runAPI(cmd, []string{"/core/v1/domains"}); err != nil {
			t.Fatalf("runAPI: %v", err)
		}
		if len(*got) != 1 || (*got)[0].method != "POST" {
			t.Errorf("sent %+v, want one POST", *got)
		}
	})

	t.Run("a GET needs no --yes", func(t *testing.T) {
		srv, got := recordServer(t, `{}`)
		cmd, _ := apiCmd(t, srv)
		withRoot(cmd, false, false)
		apiFields = []string{"perPage=2"}
		if err := runAPI(cmd, []string{"GET", "/core/v1/domains"}); err != nil {
			t.Fatalf("runAPI: %v", err)
		}
		if len(*got) != 1 {
			t.Errorf("sent %d requests, want 1", len(*got))
		}
	})
}

// TestAPI_PromptNamesInferredMethod: the question names the method and path,
// and says when the method was inferred rather than given, since that is the
// case that surprised (#282). A decline sends nothing.
func TestAPI_PromptNamesInferredMethod(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		args       []string
		set        func()
	}{
		{"-f", "Send POST (inferred from -f) /core/v1/domains?", []string{"/core/v1/domains"},
			func() { apiFields = []string{"perPage=2"} }},
		{"-F", "Send POST (inferred from -F) /core/v1/domains?", []string{"/core/v1/domains"},
			func() { apiTyped = []string{"perPage=2"} }},
		{"--data", "Send POST (inferred from --data) /core/v1/domains?", []string{"/core/v1/domains"},
			func() { apiBody = `{}` }},
		{"explicit", "Send POST /core/v1/domains?", []string{"POST", "/core/v1/domains"},
			func() { apiFields = []string{"perPage=2"} }},
		{"-X", "Send DELETE /core/v1/domains/x.com/records/1?", []string{"/core/v1/domains/x.com/records/1"},
			func() { apiMethod = "DELETE" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var asked []string
			defer cmdutil.StubConfirm(func(p string) bool { asked = append(asked, p); return false })()
			cmd, _ := apiCmd(t, refuseAll(t))
			tc.set()
			if err := runAPI(cmd, tc.args); !errors.Is(err, cmdutil.ErrAborted) {
				t.Fatalf("err = %v, want ErrAborted", err)
			}
			if len(asked) != 1 || asked[0] != tc.want {
				t.Errorf("asked %q, want %q", asked, tc.want)
			}
		})
	}
}

// TestAPI_InferredPostWarns: when -f or -F made the request a POST, a
// warning says so and how to send the fields as a query instead — on a dry
// run too, which prints no question (#282).
func TestAPI_InferredPostWarns(t *testing.T) {
	cmd, _ := apiCmd(t, refuseAll(t))
	dryRun(cmd, true)
	var stderr bytes.Buffer
	out := cmdutil.Out(cmd)
	out.Format, out.EWriter = output.FormatTable, &stderr
	apiFields = []string{"perPage=2"}
	if err := runAPI(cmd, []string{"/core/v1/domains"}); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	for _, want := range []string{"POST (inferred from -f)", "-X GET"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr.String())
		}
	}

	// An explicit method is not warned about.
	cmd, _ = apiCmd(t, refuseAll(t))
	dryRun(cmd, true)
	stderr.Reset()
	out = cmdutil.Out(cmd)
	out.Format, out.EWriter = output.FormatTable, &stderr
	apiFields = []string{"perPage=2"}
	if err := runAPI(cmd, []string{"POST", "/core/v1/domains"}); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("explicit POST warned:\n%s", stderr.String())
	}
}
