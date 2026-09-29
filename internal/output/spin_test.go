package output

import (
	"bytes"
	"reflect"
	"testing"
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
