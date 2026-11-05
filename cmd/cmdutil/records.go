package cmdutil

import (
	"fmt"
	"strings"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/spf13/cobra"
)

// ZoneRecords reads every DNS record of domain, at the largest page size so
// nearly every zone is one request. It is for a write that must say which
// records the API will replace beside it (#358): `email create` and
// `url create --host`. A missing domain is DomainNotFound.
func ZoneRecords(cmd *cobra.Command, domain string) ([]*coreapigo.Record, error) {
	stop := Out(cmd).Spin("Checking DNS records…")
	defer stop()
	client := APIClient(cmd)
	var records []*coreapigo.Record
	page, perPage := 1, MaxPerPage
	for {
		result, err := client.SDK().DNS.ListRecords(cmd.Context(),
			&coreapigo.ListRecordsRequest{DomainName: domain, Page: &page, PerPage: &perPage})
		if err != nil {
			err = api.FromSDKError(err)
			if IsNotFound(err) {
				return nil, DomainNotFound(err, domain)
			}
			return nil, err
		}
		records = append(records, NonNil(result.Records)...)
		next, ok := NextPage(page, result.NextPage, result.LastPage)
		if !ok {
			return records, nil
		}
		page = next
	}
}

// RecordHost is r's host as a zone-relative name to compare: lower case,
// no trailing dot, and "@" for the apex, which the API spells "" or "@".
func RecordHost(r *coreapigo.Record) string {
	h := strings.TrimSuffix(strings.ToLower(derefStr(r.Host)), ".")
	if h == "" {
		return "@"
	}
	return h
}

// RecordLine describes r in a few words, as a side effect names it:
// "MX @ mail.example.net (priority 10)", "A www 192.0.2.10".
func RecordLine(r *coreapigo.Record) string {
	s := fmt.Sprintf("%s %s %s", strings.ToUpper(derefStr(r.Type)), RecordHost(r), derefStr(r.Answer))
	if r.Priority != nil {
		s += fmt.Sprintf(" (priority %d)", *r.Priority)
	}
	return s
}
