package domain

import (
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"

	"github.com/patramsey/namecom-cli/internal/drifttest"
)

const domainStub = `{"domainName":"example.com","locked":true,"autorenewEnabled":true,"privacyEnabled":false,"nameservers":["ns1.name.com","ns2.name.com"],"expireDate":"2027-01-01","createDate":"2026-01-01"}`

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
		}, cmdForToggle, runLock, []string{"on", "example.com"}, domainStub)
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
