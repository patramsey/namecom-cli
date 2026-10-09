package dns

import (
	"reflect"
	"strings"
	"testing"
)

// TestDNSWrite_LowerCaseType pins #323: --type is checked without regard to
// case, but was sent as typed, and the API refused "a" with a 400 — after a
// dry run had previewed it and exited 0. It is now sent upper-cased, and the
// dry run previews what the API accepts.
func TestDNSWrite_LowerCaseType(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		z, srv := newFakeZone(t)
		cmd, _ := createFor(t, srv, runOpts{yes: true}, "--type", "txt", "--host", "lc", "--answer", "lower")
		if err := runCreate(cmd, []string{"example.com"}); err != nil {
			t.Fatalf("runCreate: %v", err)
		}
		want := []string{`POST /core/v1/domains/example.com/records {"answer":"lower","host":"lc","ttl":300,"type":"TXT"}`}
		if got := z.sentLog(); !reflect.DeepEqual(got, want) {
			t.Errorf("sent %q, want %q", got, want)
		}
	})
	t.Run("create, dry run", func(t *testing.T) {
		z, srv := newFakeZone(t)
		cmd, captured := createFor(t, srv, runOpts{yes: true, dryRun: true}, "--type", "a", "--host", "lc", "--answer", "192.0.2.1")
		if err := runCreate(cmd, []string{"example.com"}); err != nil {
			t.Fatalf("runCreate: %v", err)
		}
		if stdout, _ := captured(); !strings.Contains(stdout, `"type": "A"`) {
			t.Errorf("dry run previews %q, want type A", stdout)
		}
		if got := z.requestLog(); len(got) != 0 {
			t.Errorf("requests = %q, want none", got)
		}
	})
	t.Run("update", func(t *testing.T) {
		z, srv := newFakeZone(t, fakeRecord{ID: 5, Host: "www", Type: "A", Answer: "192.0.2.1", TTL: 300})
		cmd, _ := updateFor(t, srv, runOpts{yes: true}, "--type", "a", "--answer", "192.0.2.51")
		if err := runUpdate(cmd, []string{"example.com", "5"}); err != nil {
			t.Fatalf("runUpdate: %v", err)
		}
		want := []string{`PUT /core/v1/domains/example.com/records/5 {"answer":"192.0.2.51","host":"www","ttl":300,"type":"A"}`}
		if got := z.sentLog(); !reflect.DeepEqual(got, want) {
			t.Errorf("sent %q, want %q", got, want)
		}
	})
	t.Run("update to another type", func(t *testing.T) {
		z, srv := newFakeZone(t, fakeRecord{ID: 5, Host: "www", Type: "A", Answer: "192.0.2.1", TTL: 300})
		cmd, _ := updateFor(t, srv, runOpts{yes: true}, "--type", "cname", "--answer", "target.example.net")
		if err := runUpdate(cmd, []string{"example.com", "5"}); err != nil {
			t.Fatalf("runUpdate: %v", err)
		}
		want := []string{`PUT /core/v1/domains/example.com/records/5 {"answer":"target.example.net","host":"www","ttl":300,"type":"CNAME"}`}
		if got := z.sentLog(); !reflect.DeepEqual(got, want) {
			t.Errorf("sent %q, want %q", got, want)
		}
	})
}
