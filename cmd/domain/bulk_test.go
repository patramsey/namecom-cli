package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
)

// bulkNames is n distinct domain names, name000.com upward.
func bulkNames(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("name%03d.com", i)
	}
	return names
}

// chunkServer answers ZoneCheck and CheckAvailability for names, replying to
// each request in reverse order so only matching by name can put the rows
// back in order. Every 40th name is available, and omit names one the server
// leaves out of its reply. It records the names each request carried, and
// the path it went to.
type chunkServer struct {
	mu       sync.Mutex
	requests [][]string
	paths    []string
	omit     string
}

func (s *chunkServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		available := func(name string) bool {
			var n int
			_, _ = fmt.Sscanf(name, "name%03d.com", &n)
			return n%40 == 0
		}
		switch r.URL.Path {
		case "/core/v1/zonecheck", "/core/v1/domains:checkAvailability":
			var body struct {
				DomainNames []string `json:"domainNames"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decoding %s body: %v", r.URL.Path, err)
			}
			s.mu.Lock()
			s.requests = append(s.requests, body.DomainNames)
			s.paths = append(s.paths, r.URL.Path)
			s.mu.Unlock()
			if len(body.DomainNames) > maxCheckNames {
				http.Error(w, `{"message":"number of items must be less than or equal to 50"}`, http.StatusBadRequest)
				return
			}
			var zone []*coreapigo.ZoneCheckResult
			var reg []*coreapigo.SearchResult
			for _, name := range slices.Backward(body.DomainNames) {
				if name == s.omit {
					continue
				}
				a := available(name)
				zone = append(zone, &coreapigo.ZoneCheckResult{DomainName: name, Available: &a})
				reg = append(reg, &coreapigo.SearchResult{DomainName: name, Purchasable: a})
			}
			if r.URL.Path == "/core/v1/zonecheck" {
				_ = json.NewEncoder(w).Encode(coreapigo.ZoneCheckResponse{Results: zone})
			} else {
				_ = json.NewEncoder(w).Encode(coreapigo.SearchResponse{Results: reg})
			}
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}
}

// TestCheck_ChunksLongLists pins #244: `domain check` with 120 names failed
// with the SDK's "number of items must be less than or equal to 50". It now
// sends requests of at most 50 names — 50, 50 and 20, in order — and renders
// one row per name in the order given, on both the ZoneCheck and the
// registry path. On the ZoneCheck path each chunk's available names then go
// to the registry together, one request per chunk that has any (#294).
func TestCheck_ChunksLongLists(t *testing.T) {
	for _, authoritative := range []bool{false, true} {
		t.Run(fmt.Sprintf("authoritative=%v", authoritative), func(t *testing.T) {
			names := bulkNames(120)
			s := &chunkServer{}
			srv := httptest.NewServer(s.handler(t))
			t.Cleanup(srv.Close)

			cmd, buf := cmdForCheckJSON(t, srv)
			checkAuthoritative = authoritative
			if err := runCheck(cmd, slices.Clone(names)); err != nil {
				t.Fatalf("runCheck: %v", err)
			}

			// The first pass is ZoneCheck, or the registry when authoritative;
			// on the ZoneCheck path the registry pass follows each chunk.
			first := "/core/v1/zonecheck"
			if authoritative {
				first = "/core/v1/domains:checkAvailability"
			}
			var sizes, registry []int
			var sent []string
			for i, req := range s.requests {
				if s.paths[i] != first {
					registry = append(registry, len(req))
					continue
				}
				sizes = append(sizes, len(req))
				sent = append(sent, req...)
			}
			if !slices.Equal(sizes, []int{50, 50, 20}) {
				t.Errorf("request sizes = %v, want [50 50 20]", sizes)
			}
			if !slices.Equal(sent, names) {
				t.Error("the requests did not carry every name once, in order")
			}
			// name000 and name040, then name080: the available names, one
			// registry request per chunk that has any.
			if want := []int{2, 1}; !authoritative && !slices.Equal(registry, want) {
				t.Errorf("registry request sizes = %v, want %v", registry, want)
			}

			var got []*coreapigo.SearchResult
			if err := unmarshalData(buf.Bytes(), &got); err != nil {
				t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
			}
			if len(got) != len(names) {
				t.Fatalf("got %d rows, want %d", len(got), len(names))
			}
			for i, r := range got {
				if r.DomainName != names[i] {
					t.Fatalf("row %d is %q, want %q: rows must keep the input order", i, r.DomainName, names[i])
				}
				if want := i%40 == 0; r.Purchasable != want {
					t.Errorf("%s purchasable = %v, want %v", r.DomainName, r.Purchasable, want)
				}
			}
		})
	}
}

// TestCheck_ChunksKeepTheSafetyNet: a name the API leaves out of a later
// chunk's reply still gets a row, unpurchasable, and fails the command —
// splitting the list must not let it vanish or read as "taken".
func TestCheck_ChunksKeepTheSafetyNet(t *testing.T) {
	for _, authoritative := range []bool{false, true} {
		t.Run(fmt.Sprintf("authoritative=%v", authoritative), func(t *testing.T) {
			names := bulkNames(120)
			s := &chunkServer{omit: "name107.com"}
			srv := httptest.NewServer(s.handler(t))
			t.Cleanup(srv.Close)

			cmd, buf := cmdForCheckJSON(t, srv)
			checkAuthoritative = authoritative
			err := runCheck(cmd, slices.Clone(names))
			if err == nil || !strings.Contains(err.Error(), "name107.com") {
				t.Fatalf("runCheck = %v, want an error naming name107.com", err)
			}
			var got []*coreapigo.SearchResult
			if err := unmarshalData(buf.Bytes(), &got); err != nil {
				t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
			}
			if len(got) != len(names) || got[107].DomainName != "name107.com" || got[107].Purchasable {
				t.Errorf("want 120 rows with name107.com unpurchasable at 107, got %d rows", len(got))
			}
		})
	}
}

// TestCheck_ExitStatus pins #244: `domain check -q` exited 0 even when
// nothing was available. With --exit-status, any unavailable name fails the
// command (exit 1) after the results are printed; when every name is
// available, or without the flag, it succeeds.
func TestCheck_ExitStatus(t *testing.T) {
	for _, tc := range []struct {
		name       string
		names      []string
		exitStatus bool
		wantErr    string
	}{
		{"all available", []string{"name000.com", "name040.com"}, true, ""},
		{"one taken", []string{"name000.com", "name001.com"}, true, "1 of 2 names not available: name001.com"},
		{"many taken, across chunks", bulkNames(120), true, "117 of 120 names not available: name001.com, name002.com, name003.com, name004.com, name005.com, and 112 more"},
		{"taken without the flag", []string{"name001.com"}, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &chunkServer{}
			srv := httptest.NewServer(s.handler(t))
			t.Cleanup(srv.Close)

			cmd, buf := cmdForCheckJSON(t, srv)
			checkExitStatus = tc.exitStatus
			t.Cleanup(func() { checkExitStatus = false })
			err := runCheck(cmd, slices.Clone(tc.names))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("runCheck = %v, want success", err)
				}
			} else {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("runCheck = %v, want %q", err, tc.wantErr)
				}
				// A runtime failure (exit 1), not a usage or API error.
				if _, ok := errors.AsType[*cmdutil.UsageError](err); ok {
					t.Error("--exit-status must not be a usage error (exit 2)")
				}
				// Typed "unavailable" in the envelope, not "api" (#288).
				if _, ok := errors.AsType[*cmdutil.UnavailableError](err); !ok {
					t.Errorf("want an *UnavailableError, got %T", err)
				}
			}
			var got []*coreapigo.SearchResult
			if err := unmarshalData(buf.Bytes(), &got); err != nil || len(got) != len(tc.names) {
				t.Errorf("the results must still be printed: %v\n%s", err, buf.String())
			}
		})
	}
}
