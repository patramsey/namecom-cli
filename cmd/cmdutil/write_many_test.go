package cmdutil

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/internal/output"
)

func twoWrites() []Write[testBody] {
	return []Write[testBody]{
		{Method: "PATCH", Path: "/a", Body: testBody{Name: "a"}},
		{Method: "PATCH", Path: "/b", Body: testBody{Name: "b"}},
	}
}

// TestRunWrites_DryRunPreviewsEveryRequestAsOneDocument: a dry run of
// several writes previews them all, as one JSON array, without prompting or
// sending.
func TestRunWrites_DryRunPreviewsEveryRequestAsOneDocument(t *testing.T) {
	cmd, stdout, _ := writeCmd(t, true, false)
	Out(cmd).Format = output.FormatJSON
	failIfConfirmed(t)

	done, err := RunWrites(cmd, "Change both?", twoWrites(), failIfSent(t))
	if err != nil || done != 0 {
		t.Fatalf("RunWrites = %d, %v; want 0, nil", done, err)
	}
	var got []output.DryRunRequest
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("want one JSON array: %v\n%s", err, stdout.String())
	}
	if len(got) != 2 || got[0].Path != "/a" || got[1].Path != "/b" || !got[0].DryRun {
		t.Errorf("previewed %+v, want both requests in order", got)
	}
}

// TestRunWrites_OnePromptThenEachInOrder: one confirmation, then every body
// sent in order.
func TestRunWrites_OnePromptThenEachInOrder(t *testing.T) {
	cmd, _, _ := writeCmd(t, false, false)
	var prompts []string
	stubConfirm(t, func(_ *output.Config, _ bool, msg string) (bool, error) {
		prompts = append(prompts, msg)
		return true, nil
	})
	var sent []string
	done, err := RunWrites(cmd, "Change both?", twoWrites(), func(_ context.Context, b testBody) error {
		sent = append(sent, b.Name)
		return nil
	})
	if err != nil || done != 2 {
		t.Fatalf("RunWrites = %d, %v; want 2, nil", done, err)
	}
	if !slices.Equal(prompts, []string{"Change both?"}) || !slices.Equal(sent, []string{"a", "b"}) {
		t.Errorf("prompts %q, sent %q; want one prompt, then a and b", prompts, sent)
	}
}

// TestRunWrites_StopsAtTheFirstFailure: done counts the writes made before
// the failure, and nothing after it is sent.
func TestRunWrites_StopsAtTheFirstFailure(t *testing.T) {
	cmd, _, _ := writeCmd(t, false, true)
	writes := append(twoWrites(), Write[testBody]{Method: "PATCH", Path: "/c", Body: testBody{Name: "c"}})
	var sent []string
	boom := errors.New("boom")
	done, err := RunWrites(cmd, "", writes, func(_ context.Context, b testBody) error {
		sent = append(sent, b.Name)
		if b.Name == "b" {
			return boom
		}
		return nil
	})
	if done != 1 || !errors.Is(err, boom) {
		t.Errorf("RunWrites = %d, %v; want 1, boom", done, err)
	}
	if !slices.Equal(sent, []string{"a", "b"}) {
		t.Errorf("sent %q, want a and b only", sent)
	}
}

// TestRunWrites_OneWriteIsRunWrite: a list that came down to one target
// previews a single request object, as RunWrite does, not an array of one.
func TestRunWrites_OneWriteIsRunWrite(t *testing.T) {
	cmd, stdout, _ := writeCmd(t, true, false)
	Out(cmd).Format = output.FormatJSON
	failIfConfirmed(t)

	if _, err := RunWrites(cmd, "Change a?", twoWrites()[:1], failIfSent(t)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout.String()), "{") {
		t.Errorf("want a single JSON object, got:\n%s", stdout.String())
	}
}
