package dns

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
)

// TestDNSCreate_IDNPathIsPunycode pins issue #160 end to end for one command.
// A Unicode domain argument used to reach the URL path percent-encoded
// (/core/v1/domains/b%C3%BCcher.com/...), which name.com's edge answers with an
// HTML 403, and --dry-run previewed that same unconverted path. Both must use
// the ASCII form.
func TestDNSCreate_IDNPathIsPunycode(t *testing.T) {
	const want = "POST /core/v1/domains/xn--bcher-kva.com/records"
	const response = `{"id":1,"type":"A","host":"www","answer":"1.2.3.4","ttl":300}`
	setup := func(t *testing.T, srv *httptest.Server) *cobra.Command {
		cmd := cmdForCreate(t, srv)
		if err := cmd.ParseFlags([]string{"--type", "A", "--host", "www", "--answer", "1.2.3.4"}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		return cmd
	}
	args := []string{"bücher.com"}

	printed := captureDryRunLine(t, func(srv *httptest.Server) (*cobra.Command, error) {
		cmd := withDryRun(t, setup(t, srv), true)
		return cmd, runCreate(cmd, append([]string(nil), args...))
	}, response)
	if printed != want {
		t.Errorf("--dry-run previews %q, want %q", printed, want)
	}

	sent := captureRealRequest(t, func(srv *httptest.Server) (*cobra.Command, error) {
		cmd := withDryRun(t, setup(t, srv), false)
		return cmd, runCreate(cmd, append([]string(nil), args...))
	}, response)
	if sent != want {
		t.Errorf("sent %q, want %q", sent, want)
	}
}

// TestDNSCreate_InvalidIDNIsUsageError: a name with no valid ASCII form is a
// usage error (exit 2) and nothing is sent.
func TestDNSCreate_InvalidIDNIsUsageError(t *testing.T) {
	cmd := cmdForCreate(t, neverCalledServer(t))
	createType, createHost, createAnswer, createTTL, createPriority = "A", "@", "1.2.3.4", 300, 0

	err := runCreate(cmd, []string{"bücher-.com"})
	var ue *cmdutil.UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("runCreate(bücher-.com) = %v (%T), want *cmdutil.UsageError", err, err)
	}
}
