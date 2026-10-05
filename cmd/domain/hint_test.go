package domain

import (
	"strings"
	"testing"
	"time"

	coreapigo "github.com/namedotcom/core-api-go"
)

// TestDomainHint_FollowsState: `domain get` suggested "dns list … to manage
// DNS records" even for an expired domain, where renewing is what is needed
// (#238).
func TestDomainHint_FollowsState(t *testing.T) {
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	at := func(days int) *time.Time { d := now.AddDate(0, 0, days); return &d }
	tests := []struct {
		name string
		d    coreapigo.DomainResponsePayload
		want []string
	}{
		{"expired", coreapigo.DomainResponsePayload{DomainName: "old.com", ExpireDate: at(-73), Locked: true},
			[]string{"domain renew old.com", "expired 2 months ago"}},
		{"expiring without auto-renew", coreapigo.DomainResponsePayload{DomainName: "soon.com", ExpireDate: at(10), Locked: true},
			[]string{"domain renew soon.com", "autorenew on soon.com", "in 10 days"}},
		{"expiring with auto-renew", coreapigo.DomainResponsePayload{DomainName: "auto.com", ExpireDate: at(10), AutorenewEnabled: true, Locked: true},
			[]string{"dns list auto.com"}},
		{"healthy", coreapigo.DomainResponsePayload{DomainName: "fine.com", ExpireDate: at(400), Locked: true},
			[]string{"dns list fine.com"}},
		{"no expiry date", coreapigo.DomainResponsePayload{DomainName: "x.com"},
			[]string{"dns list x.com"}},
	}
	for _, tt := range tests {
		got := domainHint(&tt.d, now)
		for _, w := range tt.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: hint %q lacks %q", tt.name, got, w)
			}
		}
	}
}

// Success lines say what changed rather than "Updated example.com" or
// "Contacts updated for example.com" (#238).
func TestUpdateAndContactsSuccessLines(t *testing.T) {
	off, on := false, true
	req := &coreapigo.UpdateDomainRequest{DomainName: "example.com", AutorenewEnabled: &off, Locked: &on}
	if got, want := updateSummary(req), "auto-renew off, lock on"; got != want {
		t.Errorf("updateSummary = %q, want %q", got, want)
	}

	body := coreapigo.DomainsSetContactsBody{DomainName: "example.com", Contacts: &coreapigo.ContactsRequest{
		Registrant: &coreapigo.RegistrantContactRequest{}, Admin: &coreapigo.ContactRequest{},
	}}
	if got, want := contactsSetLine(body), "Replaced the registrant and admin contacts for example.com"; got != want {
		t.Errorf("contactsSetLine = %q, want %q", got, want)
	}
}
