package output

import (
	"bytes"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestRecordSpinners covers the test hook for issue #138: under go test
// stderr is not a terminal, so without it every spinner is a no-op and no
// test can tell whether a command stops the spinner it starts.
func TestRecordSpinners(t *testing.T) {
	rec, restore := RecordSpinners()
	defer restore()

	var errw bytes.Buffer
	c := &Config{Format: FormatTable, Color: ColorNever, Writer: &bytes.Buffer{}, EWriter: &errw}

	stop := c.Spin("one")
	s := c.StartSpinner("two")
	s.Update("two, updated")
	if got, want := rec.Running(), []string{"one", "two"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Running() = %v, want %v", got, want)
	}

	stop()
	stop() // idempotent, like the real stop
	s.Stop()
	s.Stop()
	if got := rec.Running(); len(got) != 0 {
		t.Errorf("Running() after stopping both = %v, want none", got)
	}
	if got, want := rec.Started(), []string{"one", "two"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Started() = %v, want %v", got, want)
	}
	if errw.Len() != 0 {
		t.Errorf("a recorded spinner must not draw, got %q on stderr", errw.String())
	}
}

// TestRecordSpinners_KeepsFormatChecks: the hook stands in for the terminal
// check only. A spinner is still not started in quiet or structured modes.
func TestRecordSpinners_KeepsFormatChecks(t *testing.T) {
	rec, restore := RecordSpinners()
	defer restore()

	for _, c := range []*Config{
		{Format: FormatJSON, EWriter: &bytes.Buffer{}},
		{Format: FormatYAML, EWriter: &bytes.Buffer{}},
		{Format: FormatTable, QuietMode: true, EWriter: &bytes.Buffer{}},
	} {
		c.Spin("x")()
		c.StartSpinner("y").Stop()
	}
	if got := rec.Started(); len(got) != 0 {
		t.Errorf("spinners started outside table mode: %v", got)
	}
}

// TestRecordSpinners_Restore: after restore, spinners are no-ops again under
// go test, so one test's hook cannot leak into the next.
func TestRecordSpinners_Restore(t *testing.T) {
	rec, restore := RecordSpinners()
	restore()

	c := &Config{Format: FormatTable, EWriter: &bytes.Buffer{}}
	c.Spin("x")()
	if got := rec.Started(); len(got) != 0 {
		t.Errorf("recorder still in use after restore: %v", got)
	}
}

// TestSpinnerNote pins the spinner half of #232: a retry printed as its own
// line landed on top of the spinner's frame. With a spinner running, the note
// goes into its text instead; without one, SpinnerNote says so and the caller
// prints a line.
func TestSpinnerNote(t *testing.T) {
	rec, restore := RecordSpinners()
	defer restore()
	c := &Config{Format: FormatTable, Color: ColorNever, Writer: &bytes.Buffer{}, EWriter: &bytes.Buffer{}}

	if SpinnerNote("rate limited") {
		t.Error("SpinnerNote reported a spinner when none was running")
	}

	stop := c.Spin("Fetching domain…")
	if !SpinnerNote("rate limited, retrying in 2s (2/3)") {
		t.Fatal("SpinnerNote did not find the running spinner")
	}
	stop()
	if SpinnerNote("x") {
		t.Error("SpinnerNote reached a stopped spinner")
	}

	s := c.StartSpinner("Importing 1/2…")
	SpinnerNote("server error (HTTP 503), retrying in 1s (1/3)")
	s.Update("Importing 2/2…") // a new step: the old note is stale
	s.Stop()

	want := []string{
		"Fetching domain…",
		"Fetching domain… rate limited, retrying in 2s (2/3)",
		"Importing 1/2…",
		"Importing 1/2… server error (HTTP 503), retrying in 1s (1/3)",
		"Importing 2/2…",
	}
	if got := rec.Shown(); !reflect.DeepEqual(got, want) {
		t.Errorf("Shown() = %q, want %q", got, want)
	}
}

// TestSpinnerNote_NotForSilentSpinners: a spinner that draws nothing — JSON
// output, --quiet, no terminal — cannot show a note, so the caller must print
// its line rather than lose it.
func TestSpinnerNote_NotForSilentSpinners(t *testing.T) {
	_, restore := RecordSpinners()
	defer restore()
	for _, c := range []*Config{
		{Format: FormatJSON, EWriter: &bytes.Buffer{}},
		{Format: FormatTable, QuietMode: true, EWriter: &bytes.Buffer{}},
	} {
		stop := c.Spin("x")
		s := c.StartSpinner("y")
		if SpinnerNote("note") {
			t.Errorf("SpinnerNote claimed a spinner that is not drawn (%+v)", c)
		}
		stop()
		s.Stop()
	}
}

// TestSpinnerNote_Drawn checks the real animation, not the recorder: the
// frame redraws with the note in place.
func TestSpinnerNote_Drawn(t *testing.T) {
	prev := spinnerTTY
	spinnerTTY = func() bool { return true }
	defer func() { spinnerTTY = prev }()

	var errw lockedBuffer
	c := &Config{Format: FormatTable, Color: ColorNever, Writer: &bytes.Buffer{}, EWriter: &errw}
	stop := c.Spin("Fetching domain…")
	defer stop()
	SpinnerNote("rate limited, retrying in 2s (2/3)")
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(errw.String(), "Fetching domain… rate limited, retrying in 2s (2/3)") {
		if time.Now().After(deadline) {
			t.Fatalf("note never drawn; stderr = %q", errw.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	if strings.Contains(errw.String(), "\n") {
		t.Errorf("spinner printed a line break: %q", errw.String())
	}
}

// lockedBuffer is a bytes.Buffer safe to read while a spinner writes to it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
