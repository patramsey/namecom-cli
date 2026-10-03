package domain

import (
	"net/http/httptest"
	"testing"

	"github.com/patramsey/namecom-cli/internal/output"

	"github.com/spf13/cobra"

	"github.com/patramsey/namecom-cli/internal/drifttest"
)

// TestDryRunMatchesRealRequest_DomainBody asserts that the body --dry-run
// prints is the body actually sent, for the two domain writes whose preview
// had drifted from it.
//
// TestDryRunMatchesRealRequest_Domain checks only the method and path, which
// both of these got right:
//
//   - `contacts set` previewed the bare contacts object, but the SDK wraps it
//     as {"contacts": {...}}, so anyone copying the preview into `namecom api`
//     sent a body the endpoint does not accept.
//   - `set-ns` previewed no body at all, then echoed the raw --ns string with
//     its whitespace intact, while the request carries a trimmed array.
func TestDryRunMatchesRealRequest_DomainBody(t *testing.T) {
	t.Run("set-ns", func(t *testing.T) {
		build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForSetNS(t, srv)
			// Spaces around the entries: the request trims them, so the
			// preview must too.
			if err := cmd.ParseFlags([]string{"--ns", "ns1.example.net, ns2.example.net"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}
		drifttest.AssertDryRunBodyMatches(t, build, runSetNS, []string{"example.com"}, domainStub)
	})

	t.Run("contacts set", func(t *testing.T) {
		const contacts = `{"registrant":{"firstName":"Ada","lastName":"Lovelace","email":"ada@example.com"}}`
		build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
			return cmdForContactsSet(t, srv, contacts)
		}
		drifttest.AssertDryRunBodyMatches(t, build, runContactsSet, []string{"example.com"}, domainStub)
	})
}

// TestDryRunMatchesRealRequest_DomainWriteBodies extends the body check to
// every other domain write, so a preview that drifts from the request fails
// here rather than in a user's terminal.
func TestDryRunMatchesRealRequest_DomainWriteBodies(t *testing.T) {
	defer output.StubInteractive(false)()

	toggles := []struct {
		name string
		args []string
		run  func(*cobra.Command, []string) error
	}{
		{"lock on", []string{"on", "example.com"}, runLock},
		{"lock off", []string{"off", "example.com"}, runLock},
		{"autorenew on", []string{"on", "example.com"}, runAutorenew},
		{"autorenew off", []string{"off", "example.com"}, runAutorenew},
		{"privacy on", []string{"on", "example.com"}, runPrivacy},
		{"privacy off", []string{"off", "example.com"}, runPrivacy},
	}
	for _, tc := range toggles {
		t.Run(tc.name, func(t *testing.T) {
			drifttest.AssertDryRunBodyMatches(t, baseCmd, tc.run, tc.args, toggleStub(tc.args[0] == "on"))
		})
	}

	t.Run("update", func(t *testing.T) {
		build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForUpdate(t, srv)
			// privacy on is the confirming path; lock off is the warning one.
			if err := cmd.ParseFlags([]string{"--privacy=true", "--lock=false"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}
		drifttest.AssertDryRunBodyMatches(t, build, runUpdate, []string{"example.com"}, domainStub)
	})

	// register and renew make several reads before they write, and drifttest
	// answers every request with one stub. Each stub below therefore carries
	// the fields of every response involved — availability, pricing, claims,
	// and the write's own response — and no key two of them disagree on.
	const registerStub = `{"results":[{"domainName":"example.com","purchasable":true,"purchasePrice":25}],` +
		`"purchasePrice":25,"renewalPrice":25,"premium":false,"claimsProcessActive":false,"claims":[],"order":1,"totalPaid":25}`
	const claimedStub = `{"results":[{"domainName":"example.com","purchasable":true,"purchasePrice":25}],` +
		`"purchasePrice":25,"premium":false,"claimsProcessActive":true,"claimId":"CLAIM-1",` +
		`"notBefore":"2026-01-01T00:00:00Z","notAfter":"2026-12-31T00:00:00Z","claims":[],"order":1,"totalPaid":25}`

	registerBuild := func(flags ...string) drifttest.Build {
		return func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForRegister(t, srv)
			if err := cmd.ParseFlags(flags); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			t.Cleanup(func() { registerYears, registerPrivacy, registerAutorenew, registerPrice = 1, false, false, 0 })
			return cmd
		}
	}

	t.Run("register", func(t *testing.T) {
		drifttest.AssertDryRunBodyMatches(t, registerBuild("--years", "2", "--privacy", "--price", "40"),
			runRegister, []string{"example.com"}, registerStub)
	})

	// The claim is the one part of register that is not a plain field: it is
	// looked up before the body is built, and acknowledged after confirmation.
	t.Run("register with a trademark claim", func(t *testing.T) {
		drifttest.AssertDryRunBodyMatches(t, registerBuild("--acknowledge-claim"),
			runRegister, []string{"example.com"}, claimedStub)
	})

	t.Run("renew", func(t *testing.T) {
		build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForRenew(t, srv)
			if err := cmd.ParseFlags([]string{"--years", "2", "--price", "40"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}
		drifttest.AssertDryRunBodyMatches(t, build, runRenew, []string{"example.com"}, registerStub)
	})
}
