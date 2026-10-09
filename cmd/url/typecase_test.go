package url

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestURLWrite_TypeAnyCase pins #323: `dns create --type a` is read as A, but
// `url create --type MASKED` was refused. --type is now read in any case on
// url create and update, and sent as the API names it.
func TestURLWrite_TypeAnyCase(t *testing.T) {
	serve := func(t *testing.T, get string, sent *[]byte, n *int) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*n++
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(get))
				return
			}
			*sent, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"id":7,"host":"www","forwardsTo":"https://example.org","type":"masked"}`))
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	sentType := func(t *testing.T, sent []byte) string {
		t.Helper()
		var body struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(sent, &body); err != nil {
			t.Fatalf("body %s: %v", sent, err)
		}
		return body.Type
	}

	t.Run("create", func(t *testing.T) {
		var sent []byte
		n := 0
		cmd := cmdForURLCreate(t, serve(t, "", &sent, &n))
		if err := cmd.ParseFlags([]string{"--to", "https://example.org", "--host", "www", "--type", "MASKED", "--title", "T"}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		if err := runCreate(cmd, []string{"example.com"}); err != nil {
			t.Fatalf("runCreate: %v", err)
		}
		if got := sentType(t, sent); got != "masked" {
			t.Errorf("sent type %q, want masked", got)
		}
		if n != 1 {
			t.Errorf("sent %d requests, want 1", n)
		}
	})
	t.Run("update", func(t *testing.T) {
		var sent []byte
		n := 0
		cmd := cmdForURLUpdate(t, serve(t, `{"id":7,"host":"www","forwardsTo":"https://example.org","type":"redirect"}`, &sent, &n))
		if err := cmd.ParseFlags([]string{"--type", "Masked", "--title", "T"}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		if err := runUpdate(cmd, []string{"example.com", "7"}); err != nil {
			t.Fatalf("runUpdate: %v", err)
		}
		if got := sentType(t, sent); got != "masked" {
			t.Errorf("sent type %q, want masked", got)
		}
		if n != 2 {
			t.Errorf("sent %d requests, want 2 (GET, PUT)", n)
		}
	})
}
