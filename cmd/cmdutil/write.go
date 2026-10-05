package cmdutil

import (
	"context"

	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// NoBody is the body type of a Write whose request carries no body a user
// would recognise — a DELETE, or a POST the SDK sends as an EmptyObject
// placeholder. RunWrite previews no body for it.
type NoBody struct{}

// Write describes one mutating API request, so that RunWrite can preview it,
// confirm it and send it from the same value.
//
// The value in Body is what --dry-run prints and what send receives. A command
// builds it once, before RunWrite is called, and never again: a preview
// assembled separately from the request — from flags, from a copy, or not at
// all — is how dry-run output has repeatedly drifted from what was sent.
type Write[B any] struct {
	// Method and Path are printed by --dry-run. The package drift tests pin
	// them against the request the command really makes.
	Method, Path string

	// Body is previewed verbatim on --dry-run and handed unchanged to send.
	// Use NoBody for a request without one.
	Body B

	// Preview, when set, derives the value --dry-run prints from Body. It
	// exists to redact secrets (a transfer's auth code) and must not do
	// anything else: every other field should reach the preview as sent.
	Preview func(B) any

	// Prompt is the confirmation question. Empty means the command does not
	// confirm. RunWrite adds the PromptContext line under it, so the question
	// need not name the account or environment. Text that quotes the request — a price, a year count — should
	// be computed from Body so the question describes what is sent.
	Prompt string

	// Spin is the spinner text shown while send runs. Empty shows none, which
	// is required when send itself may prompt.
	Spin string
}

// confirmFunc is Confirm, replaceable in tests so the decline path can be
// exercised without a terminal.
var confirmFunc = Confirm

// RunWrite runs the dry-run / confirm / send sequence every mutating command
// shares, in the one order that is correct:
//
//  1. Under --dry-run it prints the request and returns sent=false. It never
//     prompts — a dry run in CI has no terminal, and asking a human to
//     approve something that will not happen is noise — and never calls send.
//     A body the preview cannot encode is returned as the error.
//  2. Otherwise, when Prompt is set, it confirms (honouring --yes). A decline
//     prints "aborted" and returns sent=false with a nil error, so the
//     command exits 0 as it always has.
//  3. It calls send with w.Body, under the spinner if Spin is set.
//
// sent reports whether send was called. When it was, err is send's error.
// Callers render their result only when sent && err == nil.
func RunWrite[B any](cmd *cobra.Command, w Write[B], send func(ctx context.Context, body B) error) (sent bool, err error) {
	out := Out(cmd)

	if IsDryRun(cmd) {
		return false, out.DryRun(w.Method, w.Path, previewOf(w))
	}

	if w.Prompt != "" {
		ok, err := confirmFunc(out, IsYes(cmd), w.Prompt, PromptContext(cmd))
		if err != nil {
			return false, err
		}
		if !ok {
			out.Warn("aborted")
			return false, nil
		}
	}

	stop := func() {}
	if w.Spin != "" {
		stop = out.Spin(w.Spin)
	}
	err = send(cmd.Context(), w.Body)
	stop()
	// So a 5xx or an unreadable reply warns that the change may have been
	// made, which a read's error must not say.
	return true, api.MarkWrite(err)
}

// previewOf returns the value --dry-run prints for w: nothing for NoBody, the
// redacted form when Preview is set, and Body itself otherwise.
func previewOf[B any](w Write[B]) any {
	if _, ok := any(w.Body).(NoBody); ok {
		return nil
	}
	if w.Preview != nil {
		return w.Preview(w.Body)
	}
	return w.Body
}

// StubConfirm makes every RunWrite confirmation call answer with the question
// it would have asked, and returns a function that restores the real prompt.
// The stub ignores --yes. It is for tests outside this package, which cannot
// otherwise reach the decline path without a terminal:
//
//	defer cmdutil.StubConfirm(func(string) bool { return false })()
func StubConfirm(answer func(prompt string) bool) func() {
	prev := confirmFunc
	confirmFunc = func(_ *output.Config, _ bool, msg, _ string) (bool, error) {
		return answer(msg), nil
	}
	return func() { confirmFunc = prev }
}
