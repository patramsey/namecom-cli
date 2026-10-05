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

	// Quote, when set, is what the request would charge. --dry-run reports
	// it: a "quote" field in JSON and YAML, a "Would charge" line in table
	// mode. It changes nothing else.
	Quote *output.Quote

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
//     returns sent=false and ErrAborted, so the command exits 1 (#236).
//  3. It calls send with w.Body, under the spinner if Spin is set.
//
// sent reports whether send was called. When it was, err is send's error.
// Callers render their result only when sent && err == nil.
func RunWrite[B any](cmd *cobra.Command, w Write[B], send func(ctx context.Context, body B) error) (sent bool, err error) {
	out := Out(cmd)

	if IsDryRun(cmd) {
		ctx := ""
		if w.Quote != nil {
			ctx = AccountContext(cmd)
		}
		return false, out.DryRunQuote(w.Method, w.Path, previewOf(w), w.Quote, ctx)
	}

	if w.Prompt != "" {
		ok, err := confirmFunc(out, IsYes(cmd), w.Prompt, PromptContext(cmd))
		if err != nil {
			return false, err
		}
		if !ok {
			return false, ErrAborted
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

// RunWrites is RunWrite for one command acting on several targets, such as
// `dns delete D 1 2 3`. It keeps RunWrite's order, once for the whole set:
//
//  1. Under --dry-run it previews every request — one array in JSON and YAML
//     — and returns 0 without prompting or sending.
//  2. Otherwise, when prompt is set, it confirms once. The prompt should list
//     every target, since one yes approves them all. The writes' own Prompt
//     fields are not used.
//  3. It sends each write's Body in order and stops at the first failure.
//
// done is how many writes were sent successfully, so the caller can report
// writes[:done] as made; on a failure, writes[done] is the one that failed.
// A dry run or a decline returns 0, with ErrAborted for the decline. A single
// write is handed to RunWrite with prompt as its Prompt.
func RunWrites[B any](cmd *cobra.Command, prompt string, writes []Write[B], send func(ctx context.Context, body B) error) (done int, err error) {
	// One target is RunWrite exactly, so a list that came down to one name
	// previews a request object, not an array of one.
	if len(writes) == 1 {
		w := writes[0]
		w.Prompt = prompt
		sent, err := RunWrite(cmd, w, send)
		if sent && err == nil {
			return 1, nil
		}
		return 0, err
	}

	out := Out(cmd)

	if IsDryRun(cmd) {
		reqs := make([]output.DryRunRequest, len(writes))
		for i, w := range writes {
			reqs[i] = output.DryRunRequest{Method: w.Method, Path: w.Path, Body: previewOf(w)}
		}
		return 0, out.DryRunAll(reqs)
	}

	if prompt != "" {
		ok, err := confirmFunc(out, IsYes(cmd), prompt, PromptContext(cmd))
		if err != nil {
			return 0, err
		}
		if !ok {
			return 0, ErrAborted
		}
	}

	for i, w := range writes {
		stop := func() {}
		if w.Spin != "" {
			stop = out.Spin(w.Spin)
		}
		err := send(cmd.Context(), w.Body)
		stop()
		if err != nil {
			return i, api.MarkWrite(err)
		}
	}
	return len(writes), nil
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
