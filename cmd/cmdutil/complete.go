package cmdutil

import (
	"fmt"
	"strconv"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/spf13/cobra"
)

// ClientFactory builds the API client on demand. root.go stores one on the
// context of cobra's __complete command instead of a client, for two reasons.
// __complete disables flag parsing, so when persistentPreRunE runs the global
// flags (--profile, --token, --base-url, ...) are still unparsed; by the time
// a completion function runs, cobra has parsed them. And most completions are
// static — subcommand and flag names — so resolving credentials up front ran
// a token_cmd helper on every TAB for nothing.
type ClientFactory func(cmd *cobra.Command) (*api.Client, error)

// completionClient returns the client a completion function should use: one
// already on the context, else one built by the stored ClientFactory. Nil
// means there is none to be had — no credentials, an unknown profile — and
// the caller offers no candidates rather than an error mid-TAB.
func completionClient(cmd *cobra.Command) *api.Client {
	ctx := cmd.Context()
	if client, ok := ctx.Value(KeyClient).(*api.Client); ok && client != nil {
		return client
	}
	if build, ok := ctx.Value(KeyClientFactory).(ClientFactory); ok && build != nil {
		if client, err := build(cmd); err == nil {
			return client
		}
	}
	return nil
}

// CompleteDomains is a cobra ValidArgsFunction that returns domain names for
// shell tab completion. It fetches one maximally-sized page (250); cobra
// handles client-side prefix filtering from there.
func CompleteDomains(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	client := completionClient(cmd)
	if client == nil {
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
	client := completionClient(cmd)
	if client == nil {
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
