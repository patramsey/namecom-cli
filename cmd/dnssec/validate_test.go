package dnssec

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
)

// sha256Digest is a well-formed digest for digest type 2 (SHA-256).
const sha256Digest = "2BB183AF5F22588179A53B0A98631FAD1A292118B3A6E8D4C0C6F9CC6E7F4A1D"

// TestCreate_ValidatesKey pins #292: a key tag of 99999999 and a digest of
// "xyz" were previewed and sent. The key tag is 16-bit, and the digest is hex
// of the length its type produces; both are refused before any request.
func TestCreate_ValidatesKey(t *testing.T) {
	for _, tc := range []struct {
		name       string
		keyTag     int32
		digestType int32
		digest     string
		ok         bool
	}{
		{"valid SHA-256", 12345, 2, sha256Digest, true},
		{"valid SHA-1", 0, 1, strings.Repeat("a", 40), true},
		{"valid SHA-384", 65535, 4, strings.Repeat("F", 96), true},
		{"unknown type, hex", 1, 99, "abcd", true},
		{"key tag too large", 99999999, 2, sha256Digest, false},
		{"negative key tag", -1, 2, sha256Digest, false},
		{"not hex", 1, 2, "xyz", false},
		{"empty", 1, 2, "", false},
		{"SHA-256 too short", 1, 2, "abc123", false},
		{"SHA-1 given a SHA-256 digest", 1, 1, sha256Digest, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"digest":"x"}`))
			}))
			t.Cleanup(srv.Close)
			cmd := withDryRun(t, cmdForCreate(t, srv), true)
			createAlgorithm, createDigest, createDigestType, createKeyTag = 13, tc.digest, tc.digestType, tc.keyTag

			err := runCreate(cmd, []string{"example.com"})
			if tc.ok {
				if err != nil {
					t.Fatalf("want success, got %v", err)
				}
			} else if _, ok := errors.AsType[*cmdutil.UsageError](err); !ok {
				t.Fatalf("want a usage error, got %v", err)
			}
			// A valid key's dry run lists the domain's DS records (#326); an
			// invalid one is refused before any request.
			if want := map[bool]int{true: 1, false: 0}[tc.ok]; requests != want {
				t.Errorf("a dry run of create sent %d requests, want %d", requests, want)
			}
		})
	}
}

// TestCreate_DryRunChecksTheDomain pins #326: `dnssec create --dry-run` for
// a domain not in the account previewed the create and exited 0. The dry
// run lists the domain's DS records — one GET — and fails not_found as the
// create would. A real run sends the create alone.
func TestCreate_DryRunChecksTheDomain(t *testing.T) {
	for name, tc := range map[string]struct {
		dryRun bool
		status int
		want   string
	}{
		"dry run, not in the account": {true, http.StatusNotFound, "GET /core/v1/domains/example.com/dnssec"},
		"dry run, in the account":     {true, http.StatusOK, "GET /core/v1/domains/example.com/dnssec"},
		"real run":                    {false, http.StatusOK, "POST /core/v1/domains/example.com/dnssec"},
	} {
		t.Run(name, func(t *testing.T) {
			var requests []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method+" "+r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				if tc.status == http.StatusNotFound {
					_, _ = w.Write([]byte(`{"message":"Domain not found."}`))
					return
				}
				_, _ = w.Write([]byte(`{"dnssec":[],"digest":"x"}`))
			}))
			t.Cleanup(srv.Close)
			cmd := withDryRun(t, cmdForCreate(t, srv), tc.dryRun)
			createAlgorithm, createDigest, createDigestType, createKeyTag = 13, sha256Digest, 2, 12345

			err := runCreate(cmd, []string{"example.com"})
			if tc.status == http.StatusNotFound {
				if !cmdutil.IsNotFound(err) || !strings.Contains(err.Error(), "example.com") {
					t.Errorf("want not found naming the domain, got %v", err)
				}
			} else if err != nil {
				t.Errorf("runCreate: %v", err)
			}
			if got := strings.Join(requests, ","); got != tc.want {
				t.Errorf("requests = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDelete_DryRunOfMissingKey pins #292: `dnssec delete D <digest>
// --dry-run` for a domain with no such DS record previewed the delete, with
// the DS warning, and exited 0. The dry run reads the key — one GET — and
// fails not_found as the delete would. A real run sends the delete alone.
func TestDelete_DryRunOfMissingKey(t *testing.T) {
	t.Run("dry run", func(t *testing.T) {
		var requests []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests = append(requests, r.Method)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}))
		t.Cleanup(srv.Close)

		cmd := withDryRun(t, cmdWithYes(t, srv), true)
		err := runDelete(cmd, []string{"example.com", "abc123"})
		// "DS record", as the help says, not "DNSSEC key" (#293).
		if !cmdutil.IsNotFound(err) || !strings.Contains(err.Error(), "DS record abc123 not found") {
			t.Fatalf("want not found naming the DS record's digest, got %v", err)
		}
		if got := strings.Join(requests, " "); got != "GET" {
			t.Errorf("requests = %q, want one GET", got)
		}
	})

	t.Run("real run", func(t *testing.T) {
		var prompt string
		defer cmdutil.StubConfirm(func(p string) bool { prompt = p; return true })()
		var requests []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests = append(requests, r.Method)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		}))
		t.Cleanup(srv.Close)

		cmd := withDryRun(t, cmdWithYes(t, srv), false)
		if err := runDelete(cmd, []string{"example.com", "abc123"}); err != nil {
			t.Fatalf("runDelete: %v", err)
		}
		if got := strings.Join(requests, " "); got != "DELETE" {
			t.Errorf("requests = %q, want the DELETE alone", got)
		}
		if want := "Remove DS record abc123 from example.com?"; prompt != want {
			t.Errorf("prompt = %q, want %q", prompt, want)
		}
	})
}
