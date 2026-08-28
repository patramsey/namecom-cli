package cmdutil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/spf13/cobra"
)

// cmdWithClient returns a command carrying an API client pointed at srv, the
// shape completion functions expect to find on the context.
func cmdWithClient(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	cmd := &cobra.Command{}
	cmd.SetContext(context.WithValue(context.Background(), KeyClient, client))
	return cmd
}

// TestCompleteRecordIDs covers record-ID completion, which had no test at all.
//
// It pages, so it carried the same unbounded-walk bug as the list commands —
// and here the symptom is worse than a hung command: a server whose nextPage
// never advances hangs the user's shell mid-tab-completion, with no output to
// explain why and nothing obvious to interrupt.
func TestCompleteRecordIDs(t *testing.T) {
	t.Run("returns ids with a descriptive suffix", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"records":[{"id":42,"host":"www","type":"A","answer":"1.2.3.4","ttl":300}]}`))
		}))
		t.Cleanup(srv.Close)

		got, directive := CompleteRecordIDs(cmdWithClient(t, srv), "example.com")
		if directive != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("directive = %v, want NoFileComp", directive)
		}
		if len(got) != 1 {
			t.Fatalf("got %d completions, want 1: %v", len(got), got)
		}
		// zsh and fish split value from description on the tab, so the ID must
		// come first and the context after it.
		id, desc, found := strings.Cut(got[0], "\t")
		if !found {
			t.Fatalf("completion %q has no tab separator", got[0])
		}
		if id != "42" {
			t.Errorf("completion value = %q, want the bare record ID %q", id, "42")
		}
		for _, want := range []string{"A", "www", "1.2.3.4"} {
			if !strings.Contains(desc, want) {
				t.Errorf("description %q does not mention %q", desc, want)
			}
		}
	})

	t.Run("walks every page", func(t *testing.T) {
		var requests int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("page") == "2" {
				_, _ = w.Write([]byte(`{"records":[{"id":2,"host":"b","type":"A","answer":"2.2.2.2","ttl":300}],"lastPage":2}`))
				return
			}
			_, _ = w.Write([]byte(`{"records":[{"id":1,"host":"a","type":"A","answer":"1.1.1.1","ttl":300}],"nextPage":2,"lastPage":2}`))
		}))
		t.Cleanup(srv.Close)

		got, _ := CompleteRecordIDs(cmdWithClient(t, srv), "example.com")
		if len(got) != 2 {
			t.Fatalf("got %d completions across 2 pages, want 2: %v", len(got), got)
		}
		if requests != 2 {
			t.Errorf("made %d page requests, want exactly 2", requests)
		}
	})

	t.Run("a non-advancing nextPage does not hang the shell", func(t *testing.T) {
		var requests int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests++
			if requests > 10 {
				t.Errorf("completion did not terminate against a non-advancing nextPage")
				http.Error(w, "loop", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"records":[{"id":1,"host":"a","type":"A","answer":"1.1.1.1","ttl":300}],"nextPage":2,"lastPage":99}`))
		}))
		t.Cleanup(srv.Close)

		CompleteRecordIDs(cmdWithClient(t, srv), "example.com")
		if requests != 2 {
			t.Errorf("made %d requests, want 2 (page 1 -> 2, then the page stops advancing)", requests)
		}
	})

	t.Run("no client on the context degrades quietly", func(t *testing.T) {
		// Completion runs before credentials necessarily exist: root.go lets
		// initContext fail silently for __complete rather than break the shell,
		// which leaves a context with no client on it. Offering nothing is the
		// correct answer; erroring would surface in the middle of a tab.
		cmd := &cobra.Command{}
		cmd.SetContext(context.Background())
		got, directive := CompleteRecordIDs(cmd, "example.com")
		if got != nil {
			t.Errorf("got %v, want no completions", got)
		}
		if directive != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("directive = %v, want NoFileComp", directive)
		}
	})
}

// TestCompleteDomains covers domain-name completion, the sibling of
// CompleteRecordIDs above and the last completion path with no test.
//
// The cases that matter are the ones where returning nothing is correct.
// Completion runs on every tab press, so an error must degrade to "no
// suggestions" rather than printing anything: whatever a completion function
// writes to stdout, the shell tries to interpret as candidates.
func TestCompleteDomains(t *testing.T) {
	t.Run("returns domain names", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"domains":[{"domainName":"example.com"},{"domainName":"example.org"}]}`))
		}))
		t.Cleanup(srv.Close)

		got, directive := CompleteDomains(cmdWithClient(t, srv), nil, "")
		if len(got) != 2 || got[0] != "example.com" || got[1] != "example.org" {
			t.Errorf("CompleteDomains = %v, want the two domain names", got)
		}
		if directive != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("directive = %v, want NoFileComp — filenames are never valid here", directive)
		}
	})

	t.Run("suggests nothing once an argument is present", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("completion made a request when the argument was already supplied")
		}))
		t.Cleanup(srv.Close)

		got, _ := CompleteDomains(cmdWithClient(t, srv), []string{"example.com"}, "")
		if got != nil {
			t.Errorf("CompleteDomains = %v, want nil for an already-satisfied argument", got)
		}
	})

	t.Run("an API error suggests nothing rather than erroring visibly", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Authentication Failed"}`))
		}))
		t.Cleanup(srv.Close)

		got, directive := CompleteDomains(cmdWithClient(t, srv), nil, "")
		if got != nil {
			t.Errorf("CompleteDomains = %v, want nil on an API error", got)
		}
		if directive != cobra.ShellCompDirectiveError {
			t.Errorf("directive = %v, want Error", directive)
		}
	})

	// There is deliberately no "nil context" case. cmd.Context() is nil only on
	// a hand-built &cobra.Command{}; ExecuteC always sets one, so such a test
	// asserts against a command that cannot reach this function in the real
	// binary — and it fails by panicking inside cobra rather than in anything
	// this package owns.
}
