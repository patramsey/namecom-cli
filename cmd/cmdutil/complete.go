package cmdutil

import (
	"fmt"
	"strconv"

	"github.com/namedotcom/namecom-cli/internal/api"
	"github.com/namedotcom/namecom-cli/internal/api/gen"
	"github.com/spf13/cobra"
)

// CompleteDomains is a cobra ValidArgsFunction that returns all domain names
// from the account for shell tab completion.
func CompleteDomains(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	client, ok := cmd.Context().Value(KeyClient).(*api.Client)
	if !ok || client == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var names []string
	var page int32 = 1
	for {
		params := &gen.ListDomainsParams{Page: &page}
		resp, err := client.Gen().ListDomains(cmd.Context(), params)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		var result gen.ListDomainsResponseSchema
		if err := api.Decode(resp, &result); err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		for _, d := range result.Domains {
			names = append(names, d.DomainName)
		}
		if result.NextPage == nil || *result.NextPage == 0 {
			break
		}
		page = *result.NextPage
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
	var page int32 = 1
	for {
		params := &gen.ListRecordsParams{Page: &page}
		resp, err := client.Gen().ListRecords(cmd.Context(), domain, params)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		var result gen.ListRecordsResponseSchema
		if err := api.Decode(resp, &result); err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		for _, r := range result.Records {
			if r.Id == nil {
				continue
			}
			id := strconv.Itoa(int(*r.Id))
			typ, host, answer := derefStr(r.Type), derefStr(r.Host), derefStr(r.Answer)
			// "12345\tA @ → 1.2.3.4" — tab separates value from description in zsh/fish
			completions = append(completions, fmt.Sprintf("%s\t%s %s → %s", id, typ, host, answer))
		}
		if result.NextPage == nil || *result.NextPage == 0 {
			break
		}
		page = *result.NextPage
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
