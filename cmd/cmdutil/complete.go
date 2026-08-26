package cmdutil

import (
	"fmt"
	"strconv"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/spf13/cobra"
)

// CompleteDomains is a cobra ValidArgsFunction that returns domain names for
// shell tab completion. It fetches one maximally-sized page (250); cobra
// handles client-side prefix filtering from there.
func CompleteDomains(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	client, ok := cmd.Context().Value(KeyClient).(*api.Client)
	if !ok || client == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	p := 1
	perPage := 250
	result, err := client.SDK().Domains.ListDomains(cmd.Context(),
		&coreapigo.ListDomainsRequest{Page: &p, PerPage: &perPage})
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	names := make([]string, 0, len(result.Domains))
	for _, d := range result.Domains {
		names = append(names, d.DomainName)
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// CompleteRecordIDs returns DNS record IDs for the given domain, with a
// type+host description so zsh/fish can display context alongside the ID.
// Used as the second-arg completion for dns update and dns delete.
func CompleteRecordIDs(cmd *cobra.Command, domain string) ([]string, cobra.ShellCompDirective) {
	client, ok := cmd.Context().Value(KeyClient).(*api.Client)
	if !ok || client == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var completions []string
	page := 1
	for {
		result, err := client.SDK().DNS.ListRecords(cmd.Context(),
			&coreapigo.ListRecordsRequest{DomainName: domain, Page: &page})
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		for _, r := range result.Records {
			if r.ID == nil {
				continue
			}
			id := strconv.Itoa(*r.ID)
			typ, host, answer := derefStr(r.Type), derefStr(r.Host), derefStr(r.Answer)
			// "12345\tA @ → 1.2.3.4" — tab separates value from description in zsh/fish
			completions = append(completions, fmt.Sprintf("%s\t%s %s → %s", id, typ, host, answer))
		}
		next, ok := NextPage(page, result.NextPage, result.LastPage)
		if !ok {
			break
		}
		page = next
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
