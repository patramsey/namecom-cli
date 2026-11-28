package domain

import (
	"fmt"
	"strings"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
)

// purchaseTypes are the values of the SDK's SearchPurchaseType, which search,
// check and create share, in the order --purchase-type's help lists them:
// registration (standard and registry premium names) first, since it is what
// most searches mean.
var purchaseTypes = []string{
	string(coreapigo.SearchPurchaseTypeRegistration),
	string(coreapigo.SearchPurchaseTypeAftermarketS),
	string(coreapigo.SearchPurchaseTypeAftermarketB),
	string(coreapigo.SearchPurchaseTypeAftermarketI),
	string(coreapigo.SearchPurchaseTypeExpiring),
	string(coreapigo.SearchPurchaseTypeBackorder),
}

// purchaseTypeUsage is --purchase-type's help on search and check.
var purchaseTypeUsage = "show only names bought as this purchase type: " + strings.Join(purchaseTypes, ", ") +
	" (registration covers standard and registry premium names)"

// purchaseTypeArg checks a --purchase-type value, in any case, against the
// SDK's enum, and returns it ready for the request: nil when it is "".
func purchaseTypeArg(v string) (*coreapigo.SearchPurchaseType, error) {
	if v == "" {
		return nil, nil
	}
	v = strings.ToLower(strings.TrimSpace(v))
	if err := cmdutil.ValidOneOf("purchase-type", v, purchaseTypes); err != nil {
		return nil, err
	}
	pt := coreapigo.SearchPurchaseType(v)
	return &pt, nil
}

// listValues splits the values of a repeatable flag at commas, so
// `--tld io --tld com` and `--tld io,com` are the same list. Entries are
// trimmed; an empty one is an error, as an empty nameserver is in set-ns.
func listValues(values []string, flag string) ([]string, error) {
	var out []string
	for _, v := range values {
		for item := range strings.SplitSeq(v, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				return nil, cmdutil.NewUsageError(fmt.Errorf("--%s has an empty entry in %q", flag, v))
			}
			out = append(out, item)
		}
	}
	return out, nil
}

// tldList is listValues for a list of TLDs: each normalised by
// cmdutil.TLDArg (lower case, punycode, no leading dot), a repeat dropped,
// and at most max of them, the API's documented limit for the request.
func tldList(values []string, flag string, maxTLDs int) ([]string, error) {
	items, err := listValues(values, flag)
	if err != nil {
		return nil, err
	}
	var tlds []string
	seen := map[string]bool{}
	for _, item := range items {
		tld, err := cmdutil.TLDArg(item, "--"+flag)
		if err != nil {
			return nil, err
		}
		if !seen[tld] {
			seen[tld] = true
			tlds = append(tlds, tld)
		}
	}
	if len(tlds) > maxTLDs {
		return nil, cmdutil.NewUsageError(fmt.Errorf("--%s names %d TLDs; the API takes at most %d", flag, len(tlds), maxTLDs))
	}
	return tlds, nil
}

// nameserverList is listValues for a list of nameservers, checked and
// normalised as set-ns does it: lower case, a trailing dot dropped, each a
// valid hostname, none listed twice.
func nameserverList(values []string, flag string) ([]string, error) {
	items, err := listValues(values, flag)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(items))
	for i := range items {
		items[i] = strings.ToLower(strings.TrimSuffix(items[i], "."))
		if err := cmdutil.ValidNameserver(items[i], i); err != nil {
			return nil, err
		}
		if seen[items[i]] {
			return nil, cmdutil.NewUsageError(fmt.Errorf("nameserver %s is listed twice in --%s", items[i], flag))
		}
		seen[items[i]] = true
	}
	return items, nil
}
