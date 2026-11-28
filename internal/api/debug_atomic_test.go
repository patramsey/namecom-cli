package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// chunkWriter records each Write it is given as one chunk, so a test can see
// how the log split what it wrote.
type chunkWriter struct {
	mu     sync.Mutex
	chunks []string
}

func (c *chunkWriter) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.chunks = append(c.chunks, string(p))
	return len(p), nil
}

// TestDebugLog_OneWritePerEntry pins #382: --debug wrote each line of a
// request's entry in its own Write, so the header blocks of requests sent
// in parallel (`status`, `domain get A B`) interleaved and could not be
// told apart. Each request and each response is now one Write, whole.
func TestDebugLog_OneWritePerEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	var log chunkWriter
	c, err := New(Options{BaseURL: srv.URL, DebugLog: &log})
	if err != nil {
		t.Fatal(err)
	}
	const n = 4 // within the limiter's burst, so nothing waits
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			resp, err := c.HTTPClient().Get(fmt.Sprintf("%s/core/v1/domains/d%d.com", srv.URL, i))
			if err != nil {
				t.Error(err)
				return
			}
			_ = resp.Body.Close()
		})
	}
	wg.Wait()

	if len(log.chunks) != 2*n {
		t.Fatalf("%d writes, want %d (a request and a response each):\n%s", len(log.chunks), 2*n, strings.Join(log.chunks, "---\n"))
	}
	for _, ch := range log.chunks {
		switch {
		case strings.Contains(ch, "→ GET"):
			if !strings.Contains(ch, "\n  User-Agent: ") {
				t.Errorf("a request entry without its headers:\n%s", ch)
			}
		case strings.Contains(ch, "← 200"):
			if !strings.Contains(ch, "\n  Content-Type: application/json") || !strings.Contains(ch, "\n  body: {}") {
				t.Errorf("a response entry without its headers and body:\n%s", ch)
			}
		default:
			t.Errorf("an entry that is neither a request nor a response:\n%s", ch)
		}
	}
}
