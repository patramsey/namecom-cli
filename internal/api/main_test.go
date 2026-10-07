package api

import (
	"os"
	"testing"
	"time"
)

// TestMain shortens the default retry backoff so tests that drive real
// retries through New() finish in milliseconds instead of seconds. Tests of
// the backoff arithmetic set baseDelay on their transport explicitly.
func TestMain(m *testing.M) {
	defaultBaseDelay = time.Millisecond
	os.Exit(m.Run())
}
