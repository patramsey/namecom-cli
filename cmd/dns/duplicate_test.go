package dns

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
)

// errorHintOf is the error.hint the JSON error envelope carries for err.
func errorHintOf(t *testing.T, err error) string {
	t.Helper()
	var buf bytes.Buffer
	(&output.Config{Format: output.FormatJSON, EWriter: &buf}).ErrorWith(err, output.ErrorInfo{Type: output.ErrorTypeConflict})
	var doc struct {
		Error struct {
			Hint string `json:"hint"`
		} `json:"error"`
	}
	if jerr := json.Unmarshal(buf.Bytes(), &doc); jerr != nil {
		t.Fatalf("error envelope %q: %v", buf.String(), jerr)
	}
	return doc.Error.Hint
}

// TestDNSWrite_DuplicateHintsAtTheIdempotentFlag pins #323: a create the API
// refused as "Record already exists" said only that, although the command
// has a flag that makes an existing record a success. The hint now names it,
// and the API error stays reachable for the exit code and error type. With
// that flag already passed, the duplicate is a race and the hint is not
// given.
func TestDNSWrite_DuplicateHintsAtTheIdempotentFlag(t *testing.T) {
	existing := fakeRecord{ID: 5, Host: "dup", Type: "A", Answer: "192.0.2.77", TTL: 300}
	checkAPI := func(t *testing.T, err error) {
		t.Helper()
		if apiErr, ok := errors.AsType[*api.APIError](err); !ok || apiErr.StatusCode != http.StatusBadRequest {
			t.Errorf("err = %v (%T), want the API's 400 reachable", err, err)
		}
	}

	t.Run("create", func(t *testing.T) {
		z, srv := newFakeZone(t, existing)
		cmd, _ := createFor(t, srv, runOpts{yes: true}, "--type", "A", "--host", "dup", "--answer", "192.0.2.77")
		err := runCreate(cmd, []string{"example.com"})
		checkAPI(t, err)
		if got, want := errorHintOf(t, err), "pass --if-not-exists to treat an existing record as success"; got != want {
			t.Errorf("hint = %q, want %q", got, want)
		}
		if got, want := z.requestLog(), []string{"POST /core/v1/domains/example.com/records"}; !reflect.DeepEqual(got, want) {
			t.Errorf("requests = %q, want %q", got, want)
		}
	})
	t.Run("import", func(t *testing.T) {
		z, srv := newFakeZone(t, existing)
		file := writeFile(t, "r.json", `[{"type":"A","host":"dup","answer":"192.0.2.77","ttl":300},{"type":"A","host":"new","answer":"192.0.2.78","ttl":300}]`)
		_, _, err := runImportFile(t, srv, runOpts{yes: true}, file, false)
		checkAPI(t, err)
		if got, want := errorHintOf(t, err), "pass --skip-existing to skip records already in the zone"; got != want {
			t.Errorf("hint = %q, want %q", got, want)
		}
		if got, want := z.requestLog(), []string{"POST /core/v1/domains/example.com/records"}; !reflect.DeepEqual(got, want) {
			t.Errorf("requests = %q, want %q", got, want)
		}
	})
	t.Run("another 400 gets no such hint", func(t *testing.T) {
		z, srv := newFakeZone(t)
		z.fail = func(string, fakeRecord) bool { return true }
		cmd, _ := createFor(t, srv, runOpts{yes: true}, "--type", "A", "--host", "x", "--answer", "192.0.2.1")
		err := runCreate(cmd, []string{"example.com"})
		if got := errorHintOf(t, err); got == "pass --if-not-exists to treat an existing record as success" {
			t.Errorf("hint = %q for a refusal that is not a duplicate", got)
		}
	})
}
