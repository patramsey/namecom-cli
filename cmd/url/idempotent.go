package url

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/spf13/cobra"
)

// forwardingType reads a --type value: in any case, as `dns create --type`
// reads it (#323), and with "301" for "redirect", since the help calls the
// default a 301 and "302" is a value (#361).
func forwardingType(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "301" {
		return "redirect"
	}
	return s
}

// hostKey is a forwarding's host in one form to compare: lower case,
// relative to domain, and "@" for the apex however the API spells it — "",
// "@" or the domain itself.
func hostKey(h, domain string) string {
	h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
	switch {
	case h == "" || h == "@" || h == domain:
		return "@"
	case strings.HasSuffix(h, "."+domain):
		return strings.TrimSuffix(h, "."+domain)
	}
	return h
}

// listForwardings is every forwarding on domain, read at the largest page
// size. A domain not in the account is a not-found error naming it.
func listForwardings(cmd *cobra.Command, domain string) ([]*coreapigo.URLForwardingResponse, error) {
	client := cmdutil.APIClient(cmd)
	var all []*coreapigo.URLForwardingResponse
	page, perPage := 1, cmdutil.MaxPerPage
	for {
		result, err := client.SDK().URLForwardings.ListURLForwardingsByDomain(cmd.Context(),
			&coreapigo.ListURLForwardingsByDomainRequest{DomainName: domain, Page: &page, PerPage: &perPage})
		if err != nil {
			if cmdutil.IsNotFound(err) {
				return nil, cmdutil.DomainNotFound(err, domain)
			}
			return nil, api.FromSDKError(err)
		}
		all = append(all, cmdutil.NonNil(result.URLForwarding)...)
		next, ok := cmdutil.NextPage(page, result.NextPage, result.LastPage)
		if !ok {
			return all, nil
		}
		page = next
	}
}

// existingOnHost is `url create --if-not-exists` looking for the host's
// forwarding: the one on host, or nil when it has none.
func existingOnHost(cmd *cobra.Command, domain, host string) (*coreapigo.URLForwardingResponse, error) {
	stop := cmdutil.Out(cmd).Spin("Checking for an existing forwarding…")
	all, err := listForwardings(cmd, domain)
	stop()
	if err != nil {
		return nil, err
	}
	want := hostKey(host, domain)
	for _, f := range all {
		if f.ID != nil && hostKey(derefStr(f.Host), domain) == want {
			return f, nil
		}
	}
	return nil, nil
}

// sameForwarding reports whether existing already is what body asks for:
// the same destination and type, and for masked forwarding the same title
// and meta.
func sameForwarding(existing *coreapigo.URLForwardingResponse, body coreapigo.URLForwardingInput) bool {
	if existing.ForwardsTo != body.ForwardsTo || string(existing.Type) != string(body.Type) {
		return false
	}
	if string(body.Type) != "masked" {
		return true
	}
	return derefStr(existing.Title) == derefStr(body.Title) && derefStr(existing.Meta) == derefStr(body.Meta)
}

// updateCommand is the `url update` that makes existing what body asks for.
func updateCommand(domain string, id int, body coreapigo.URLForwardingInput) string {
	s := fmt.Sprintf("namecom url update %s %d --to %s --type %s", domain, id, shellQuote(body.ForwardsTo), body.Type)
	if body.Title != nil {
		s += " --title " + shellQuote(*body.Title)
	}
	if body.Meta != nil {
		s += " --meta " + shellQuote(*body.Meta)
	}
	return s
}

// shellQuote quotes s for a POSIX shell when it needs it, so a suggested
// command can be pasted as is: a destination with "?" or "&" in it.
func shellQuote(s string) string {
	safe := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-+=:,@~", r)
	}
	if s != "" && strings.IndexFunc(s, func(r rune) bool { return !safe(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// hostTaken is the conflict of a create for a host that already forwards
// elsewhere, found by --if-not-exists: the API would refuse it with a 409.
// It names the forwarding's ID and the update that changes it.
func hostTaken(domain, host string, existing *coreapigo.URLForwardingResponse, body coreapigo.URLForwardingInput) error {
	id := *existing.ID
	return &cmdutil.ConflictError{
		Err: fmt.Errorf("%s already forwards to %s (%s, id %d): nothing was created",
			forwardingName(domain, hostKey(host, domain)), existing.ForwardsTo, existing.Type, id),
		Hint:    fmt.Sprintf("run '%s' to change it", updateCommand(domain, id, body)),
		Details: existing,
	}
}

// existsError is the API's 409 for a create on a host that already has a
// forwarding, with the commands that deal with it: the API said only
// "Conflict" (#361). It wraps the API error, so the exit code and the
// envelope's "conflict" type are unchanged.
type existsError struct {
	err  error
	hint string
}

func (e *existsError) Error() string    { return e.err.Error() }
func (e *existsError) Unwrap() error    { return e.err }
func (e *existsError) UserHint() string { return e.hint }

// hintDuplicate gives a create's 409 the hint `dns create` and `email
// create` give their duplicates, and returns any other error as it is. A
// 409 about an idempotency key is left alone: it is not about the host.
func hintDuplicate(err error, domain, host string) error {
	apiErr, ok := errors.AsType[*api.APIError](api.NormalizeError(err))
	if !ok || apiErr.StatusCode != http.StatusConflict ||
		strings.Contains(strings.ToLower(apiErr.Message+" "+apiErr.Details), "idempotency") {
		return err
	}
	return &existsError{err: err, hint: fmt.Sprintf(
		"%s probably has a forwarding already: run 'namecom url list %s' for its ID and 'namecom url update %s <id>' to change it, or pass --if-not-exists to accept one that already forwards there",
		forwardingName(domain, host), domain, domain)}
}
