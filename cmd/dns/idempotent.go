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
	h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
	if h == "@" || h == domain {
		return "", nil
	}
	h = strings.TrimSuffix(h, "."+domain)
	a, err := cmdutil.ASCIIHostname(h, "--host")
	if err != nil {
		return "", err
	}
	return normHost(a), nil
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
			return nil, cmdutil.NotFound(err, fmt.Sprintf("domain %q not found — run 'namecom domain list' to see your domains", domain))
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
	case output.FormatJSON:
		return out.JSON(existing)
	case output.FormatYAML:
		return out.YAML(existing)
	}
	out.Success(fmt.Sprintf("%s already exists on %s (id %d): nothing created", recordSummary(existing), body.DomainName, id))
	return nil
}

// deleteAbsent is `dns delete --if-exists` for a record the API says is not
// there. The record's 404 could also mean the domain is missing, which
// --if-exists must not hide — a typo in the domain would otherwise "succeed"
// — so the domain is checked with a one-record list first.
func deleteAbsent(cmd *cobra.Command, domain string, id int) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	one, page := 1, 1
	if _, err := client.SDK().DNS.ListRecords(cmd.Context(), &coreapigo.ListRecordsRequest{
		DomainName: domain, Page: &page, PerPage: &one,
	}); err != nil {
		err = api.FromSDKError(err)
		if cmdutil.IsNotFound(err) {
			return cmdutil.NotFound(err, fmt.Sprintf("domain %q not found — run 'namecom domain list' to see your domains", domain))
		}
		return err
	}
	out.Success(fmt.Sprintf("Record %d is not on %s: nothing to delete", id, domain))
	return nil
}
