package domain

import (
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/spf13/cobra"

	"github.com/patramsey/namecom-cli/internal/drifttest"
)

const domainStub = `{"domainName":"example.com","locked":true,"autorenewEnabled":true,"privacyEnabled":false,"nameservers":["ns1.name.com","ns2.name.com"],"expireDate":"2027-01-01","createDate":"2026-01-01"}`

// toggleStub is a domain whose three toggles are all the opposite of enable.
// A toggle already in the requested state reads the domain and sends nothing
// (#187), so a test of the request a toggle sends needs one that changes.
func toggleStub(enable bool) string {
	v := strconv.FormatBool(!enable)
	return `{"domainName":"example.com","locked":` + v + `,"autorenewEnabled":` + v + `,"privacyEnabled":` + v + `}`
}

// TestRequestShape_Domain pins the wire request for every mutating command in
// this group, captured from the generated client before the port to the Core
// SDK (#40).
//
// This is the group where getting it wrong costs money: register and renew are
// purchases, and the toggles change what a domain does. Slices 3, 4, and 5 each
// turned up a request the SDK could not express identically, so these exist to
// make any such change here fail loudly rather than ship.
func TestRequestShape_Domain(t *testing.T) {
	t.Run("lock on", func(t *testing.T) {
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "PATCH", Path: "/core/v1/domains/example.com", Body: `{"locked":true}`,
		}, cmdForToggle, runLock, []string{"on", "example.com"}, toggleStub(true))
	})

	t.Run("autorenew off", func(t *testing.T) {
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "PATCH", Path: "/core/v1/domains/example.com", Body: `{"autorenewEnabled":false}`,
		}, cmdForToggle, runAutorenew, []string{"off", "example.com"}, domainStub)
	})

	t.Run("privacy on", func(t *testing.T) {
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "PATCH", Path: "/core/v1/domains/example.com", Body: `{"privacyEnabled":true}`,
		}, cmdForToggle, runPrivacy, []string{"on", "example.com"}, domainStub)
	})

	t.Run("set-ns", func(t *testing.T) {
		build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForSetNS(t, srv)
			if err := cmd.ParseFlags([]string{"--ns", "ns1.example.net,ns2.example.net"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "POST", Path: "/core/v1/domains/example.com:setNameservers", Body: `{"nameservers":["ns1.example.net","ns2.example.net"]}`,
		}, build, runSetNS, []string{"example.com"}, domainStub)
	})

	// domain update sends only the fields whose flags were passed (#116).
	//
	// It used to read the domain and restate all three fields, so every update
	// carried the current `locked` — and during the 60-day transfer lock after
	// registration or transfer the API rejects any request containing `locked`,
	// even an unchanged true. `domain update --autorenew=false` failed with
	// "Domain can not be unlocked until …" although --lock was never passed.
	//
	// UpdateDomain is a PATCH that takes "one, or any combination of the
	// parameters", and since SDK v1.33.5 each field is a flat *bool with
	// omitempty, so a field left nil is left alone. domainStub has locked and
	// autorenewEnabled true: a body restating current state would show them.
	//
	// Before v1.33.5 the three could not be sent together at all — the SDK
	// modelled them as an exclusive union that kept only the first — so the
	// combinations also pin that every passed flag reaches the wire. See
	// docs/upstream/core-api-go-updatedomain-union.md.
	//
	// Every flag here differs from domainStub: a field already in the
	// requested state is left out (#287).
	for _, tc := range []struct {
		name  string
		flags []string
		body  string
	}{
		{"update --autorenew alone", []string{"--autorenew=false"}, `{"autorenewEnabled":false}`},
		{"update --privacy alone", []string{"--privacy=true"}, `{"privacyEnabled":true}`},
		{"update --lock alone", []string{"--lock=false"}, `{"locked":false}`},
		{"update --autorenew --privacy", []string{"--autorenew=false", "--privacy=true"},
			`{"autorenewEnabled":false,"privacyEnabled":true}`},
		{"update all three", []string{"--autorenew=false", "--privacy=true", "--lock=false"},
			`{"autorenewEnabled":false,"privacyEnabled":true,"locked":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
				cmd := cmdForUpdate(t, srv)
				if err := cmd.ParseFlags(tc.flags); err != nil {
					t.Fatalf("ParseFlags: %v", err)
				}
				return cmd
			}
			drifttest.AssertRequest(t, drifttest.Request{
				Method: "PATCH", Path: "/core/v1/domains/example.com", Body: tc.body,
			}, build, runUpdate, []string{"example.com"}, domainStub)
		})
	}

	t.Run("register", func(t *testing.T) {
		// register checks availability and price before purchasing, and
		// drifttest answers every request with one stub, so this document has
		// to satisfy the availability decode, the pricing decode, and the
		// create response. Only the last request — the POST — is asserted.
		const registerStub = `{"results":[{"domainName":"example.com","purchasable":true,` +
			`"purchasePrice":12.99,"purchaseType":"registration"}],` +
			`"purchasePrice":12.99,"domainName":"example.com","expireDate":"2028-01-01"}`

		build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForRegister(t, srv)
			if err := cmd.ParseFlags([]string{"--years", "2"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "POST", Path: "/core/v1/domains", Body: `{"domain":{"autorenewEnabled":false,"domainName":"example.com","privacyEnabled":false},"years":2}`,
		}, build, runRegister, []string{"example.com"}, registerStub)
	})

	t.Run("renew", func(t *testing.T) {
		build := func(t *testing.T, srv *httptest.Server) *cobra.Command {
			cmd := cmdForRenew(t, srv)
			if err := cmd.ParseFlags([]string{"--years", "3"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			return cmd
		}
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "POST", Path: "/core/v1/domains/example.com:renew", Body: `{"years":3}`,
		}, build, runRenew, []string{"example.com"}, domainStub)
	})
}
