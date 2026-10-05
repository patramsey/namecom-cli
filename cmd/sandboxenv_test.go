package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
)

// TestSandboxEnv_InvalidIsUsageError guards #225 end to end: an unrecognized
// NAMECOM_SANDBOX stops the command with exit 2 before any request is sent,
// instead of being read as false and sending the request to production.
func TestSandboxEnv_InvalidIsUsageError(t *testing.T) {
	withConfig(t, loneProfile)
	t.Setenv("NAMECOM_SANDBOX", "garbage")
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("an invalid NAMECOM_SANDBOX still sent %s %s", r.Method, r.URL)
	}))
	t.Cleanup(srv.Close)
	prev := gf
	gf = globalFlags{baseURL: srv.URL}
	t.Cleanup(func() { gf = prev })

	for name, run := range map[string]func() error{
		"API command setup": func() error { cmd, _ := jsonCmd(t); return initContext(cmd) },
		"auth status":       func() error { cmd, _ := jsonCmd(t); return runAuthStatus(cmd, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			err := run()
			if err == nil {
				t.Fatal("accepted NAMECOM_SANDBOX=garbage")
			}
			if got := exitCode(err); got != 2 {
				t.Errorf("exit code = %d, want 2 (usage): %v", got, err)
			}
			if !strings.Contains(err.Error(), `NAMECOM_SANDBOX="garbage"`) {
				t.Errorf("error does not name the variable and value: %v", err)
			}
		})
	}
}

// TestSandboxEnv_YesSelectsSandbox: the spelling a CI job reached for to stay
// safe now does what it says.
func TestSandboxEnv_YesSelectsSandbox(t *testing.T) {
	withConfig(t, loneProfile)
	t.Setenv("NAMECOM_SANDBOX", "yes")
	prev := gf
	gf = globalFlags{}
	t.Cleanup(func() { gf = prev })

	cmd, _ := jsonCmd(t)
	if err := initContext(cmd); err != nil {
		t.Fatalf("initContext: %v", err)
	}
	if got := cmdutil.APIClient(cmd).BaseURL(); got != api.DefaultBaseURL(true) {
		t.Errorf("NAMECOM_SANDBOX=yes selected %s", got)
	}
	if !cmdutil.Out(cmd).Sandbox {
		t.Error("NAMECOM_SANDBOX=yes did not mark the output config as sandbox")
	}
}
