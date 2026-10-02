package cmdutil

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/spf13/cobra"
)

// CompletionTimeout bounds the API work behind one TAB. The shell is frozen
// until completion returns, so a slow or unreachable API should cost the user
// a moment, not the full --timeout; root.go also disables retries for the
// completion client.
const CompletionTimeout = 2 * time.Second

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
// shell tab completion. It fetches one maximally-sized page (250), filtered
// server-side by what has been typed so far — without the filter, a domain
// past the first page of a large account could never be completed. The shell
// narrows the matches to the prefix from there.
func CompleteDomains(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	client := completionClient(cmd)
	if client == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), CompletionTimeout)
	defer cancel()
	p := 1
	perPage := 250
	req := &coreapigo.ListDomainsRequest{Page: &p, PerPage: &perPage}
	if toComplete != "" {
		// The same wrapping as `domain list --filter`: the API takes a
		// wildcard only when it starts with '*', and one the user typed
		// passes through.
		f := toComplete
		if !strings.Contains(f, "*") {
			f = "*" + f + "*"
		}
		req.DomainName = &f
	}
	result, err := client.SDK().Domains.ListDomains(ctx, req)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	names := make([]string, 0, len(result.Domains))
	for _, d := range NonNil(result.Domains) {
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
	// One deadline for the whole walk, not one per page.
	ctx, cancel := context.WithTimeout(cmd.Context(), CompletionTimeout)
	defer cancel()
	var completions []string
	page := 1
	for {
		result, err := client.SDK().DNS.ListRecords(ctx,
			&coreapigo.ListRecordsRequest{DomainName: domain, Page: &page})
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		for _, r := range NonNil(result.Records) {
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

// CompleteProfiles offers the profile names in the config file, for --profile
// and `config use`. It reads the file only: no credential is resolved and no
// token_cmd runs. Without it these fell back to filename completion (#187).
func CompleteProfiles(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	f, err := config.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(f.Profiles))
	for name := range f.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, cobra.ShellCompDirectiveNoFileComp
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
