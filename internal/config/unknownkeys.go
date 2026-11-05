package config

import (
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// warnUnknownKeys raises a warning, for TakeWarnings, for every key in the
// config file that namecom does not read: at the top level and in each
// profile (#358). They were ignored silently, so a profile's `base_url` sent
// its token to production as though no profile had chosen otherwise, and a
// `sandox: true` did the same to a user who believed they were on the
// sandbox. A warning rather than an error: Save keeps keys it does not model,
// and another tool may share the file.
//
// The keys namecom writes are the struct tags (fileKeys, profileKeys), so a
// file the CLI wrote never warns. A document that is not a mapping is left to
// yaml.Unmarshal, which has already accepted or refused it.
func warnUnknownKeys(path string, data []byte) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
		return
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, val := root.Content[i].Value, root.Content[i+1]
		if !slices.Contains(fileKeys, key) {
			addWarning(unknownKeyMsg(path, key, "", fileKeys))
			continue
		}
		if key != "profiles" || val.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(val.Content); j += 2 {
			name, prof := val.Content[j].Value, val.Content[j+1]
			if prof.Kind != yaml.MappingNode {
				continue
			}
			for k := 0; k+1 < len(prof.Content); k += 2 {
				if pk := prof.Content[k].Value; !slices.Contains(profileKeys, pk) {
					addWarning(unknownKeyMsg(path, pk, name, profileKeys))
				}
			}
		}
	}
}

// unknownKeyMsg words the warning for key, which is in profile when that is
// not empty and at the top level otherwise.
func unknownKeyMsg(path, key, profile string, known []string) string {
	where := "at the top level of " + path
	if profile != "" {
		where = fmt.Sprintf("in profile %q of %s", profile, path)
	}
	msg := fmt.Sprintf("unknown config key %q %s is ignored", key, where)
	switch {
	case endpointKey(key):
		return msg + "; the API endpoint is set with --base-url or NAMECOM_BASE_URL, not in the config file"
	case closest(key, known) != "":
		return msg + fmt.Sprintf("; did you mean %q?", closest(key, known))
	}
	return msg + "; known keys are " + strings.Join(known, ", ")
}

// endpointKey reports whether key looks like an attempt to set the API
// endpoint: base_url, baseURL, endpoint, api_url, server and the like.
func endpointKey(key string) bool {
	k := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(key))
	return strings.Contains(k, "url") || strings.Contains(k, "endpoint") ||
		k == "server" || k == "api" || k == "apibase" || k == "baseuri"
}

// closest is the known key key is most likely a misspelling of — the same
// key in another case or with "-" for "_", or one at most two edits away —
// or "" when none is that close.
func closest(key string, known []string) string {
	norm := func(s string) string { return strings.ReplaceAll(strings.ToLower(s), "-", "_") }
	best, bestDist := "", 3
	for _, k := range known {
		if norm(k) == norm(key) {
			return k
		}
		if d := editDistance(norm(key), norm(k)); d < bestDist {
			best, bestDist = k, d
		}
	}
	return best
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
