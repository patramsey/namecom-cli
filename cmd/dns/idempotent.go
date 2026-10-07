package dns

import (
	"fmt"
	"strconv"
	"strings"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// filterHost is the --host value of `dns list` in the form normHost gives a
// record's host: "" for the apex ("@" or the domain itself), and otherwise
// the labels before the domain, so "www" and "www.example.com." both match
// the record for www.
func filterHost(h, domain string) (string, error) {
	z, err := zoneHost(strings.TrimSpace(h), domain)
	if err != nil {
		return "", err
	}
	return normHost(z), nil
}

// zoneHost is a --host value in the form the API takes, validated and with
// Unicode labels in punycode: relative to the zone, and "@" for the apex.
// `dns list`, `dns create` and `dns update` all read it this way, so one
// value names one record in each (#285). "www", "www.example.com" and
// "www.example.com." are all www; "example.com" is the apex. Create used to
// send a fully qualified host as typed, and the API made
// www.example.com.example.com.
func zoneHost(h, domain string) (string, error) {
	return asciiHost(relHost(h, domain))
}

// relHost strips the zone from a fully qualified host, with or without its
// trailing dot, and makes the domain itself "@". Any other host is returned
// without a trailing dot, and is left to asciiHost to validate.
func relHost(h, domain string) string {
	t := strings.TrimSuffix(h, ".")
	if t == "" {
		return h
	}
	a, err := cmdutil.ASCIIHostname(t, "--host")
	if err != nil {
		return t
	}
	switch la := strings.ToLower(a); {
	case la == domain:
		return "@"
	case strings.HasSuffix(la, "."+domain):
		return a[:len(a)-len(domain)-1]
	}
	return t
}

// findRecord returns the live record on domain with this host, type and
// answer, compared as `dns sync` compares them, or nil when there is none.
func findRecord(cmd *cobra.Command, domain, rtype, host, answer string) (*coreapigo.Record, error) {
	out := cmdutil.Out(cmd)
	stop := out.Spin("Checking for an existing record…")
	live, _, _, err := fetchRecords(cmd, domain, 1, nil, true)
	stop()
	if err != nil {
		if cmdutil.IsNotFound(err) {
			return nil, cmdutil.DomainNotFound(err, domain)
		}
		return nil, err
	}
	want := keyOf(rtype, host, answer)
	for _, r := range live {
		if r != nil && liveKey(r) == want {
			return r, nil
		}
	}
	return nil, nil
}

// reportExisting is `dns create --if-not-exists` finding the record already
// there: it prints the existing record as create prints a new one — its ID
// under --quiet, the record in JSON and YAML — and exits 0. A TTL or
// priority that differs from what was asked for is warned about, not
// changed; that is `dns update`'s job.
func reportExisting(cmd *cobra.Command, existing *coreapigo.Record, body coreapigo.DNSCreateRecordBody) error {
	out := cmdutil.Out(cmd)
	id := derefInt(existing.ID)
	var differs []string
	if body.TTL != nil && *body.TTL != existing.TTL {
		differs = append(differs, fmt.Sprintf("TTL %d, not %d", existing.TTL, *body.TTL))
	}
	if typeHasPriority(strings.ToUpper(string(body.Type))) && body.Priority != nil && derefInt64(existing.Priority) != *body.Priority {
		differs = append(differs, fmt.Sprintf("priority %d, not %d", derefInt64(existing.Priority), *body.Priority))
	}
	if len(differs) > 0 {
		out.Warn(fmt.Sprintf("the existing record has %s — run 'namecom dns update %s %d' to change it",
			strings.Join(differs, " and "), body.DomainName, id))
	}

	if out.QuietMode {
		out.Quiet(strconv.Itoa(id))
		return nil
	}
	switch out.Format {
	case output.FormatJSON, output.FormatYAML:
		return printCreated(out, existing, false)
	}
	out.Success(fmt.Sprintf("%s already exists on %s (id %d): nothing created", recordSummary(existing), body.DomainName, id))
	return nil
}

// printCreated prints the record `dns create` made, or with --if-not-exists
// found, in JSON or YAML. Under --if-not-exists the record carries "changed":
// false when it was already there and true when it was created, so a script
// can tell which (#240); without the flag the record is printed as it is.
func printCreated(out *output.Config, rec *coreapigo.Record, changed bool) error {
	var doc any = rec
	if createIfNotExists {
		withChanged, err := output.WithChanged(rec, changed)
		if err != nil {
			return err
		}
		doc = withChanged
	}
	if out.Format == output.FormatYAML {
		return out.YAML(doc)
	}
	return out.JSON(doc)
}

// deleteAbsent is `dns delete --if-exists` when the API says none of the
// records is there. A record's 404 could also mean the domain is missing,
// which --if-exists must not hide — a typo in the domain would otherwise
// "succeed" — so the domain is checked with a one-record list first.
func deleteAbsent(cmd *cobra.Command, domain string, ids []int) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	one, page := 1, 1
	if _, err := client.SDK().DNS.ListRecords(cmd.Context(), &coreapigo.ListRecordsRequest{
		DomainName: domain, Page: &page, PerPage: &one,
	}); err != nil {
		err = api.FromSDKError(err)
		if cmdutil.IsNotFound(err) {
			return cmdutil.DomainNotFound(err, domain)
		}
		return err
	}
	// "changed": false in JSON and YAML, one document however many IDs.
	res := out.Results()
	for _, id := range ids {
		res.Add(output.ResultItem{Domain: domain, ID: id, Message: fmt.Sprintf("Record %d is not on %s: nothing to delete", id, domain)})
	}
	res.Print(fmt.Sprintf("None of the %d records is on %s: nothing to delete", len(ids), domain))
	return nil
}
