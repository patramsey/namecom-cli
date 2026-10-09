package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/spf13/cobra"
)

// writeInvocations is one successful run of every write command, against
// apiStub's defaults (or routes), keyed by the command's path.
func writeInvocations(t *testing.T) map[string]struct {
	args   []string
	routes map[string]reply
} {
	t.Helper()
	dir := t.TempDir()
	recordsFile := filepath.Join(dir, "records.json")
	contactsFile := filepath.Join(dir, "contacts.json")
	for name, body := range map[string]string{
		recordsFile:  `[{"type":"A","host":"www","answer":"192.0.2.42","ttl":300},{"type":"A","host":"api","answer":"192.0.2.9","ttl":300}]`,
		contactsFile: `{"registrant":{"firstName":"A","lastName":"B","email":"a@example.com"}}`,
	} {
		if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	unlocked := reply{200, `{"domainName":"example.com","locked":false,"autorenewEnabled":false,"privacyEnabled":false}`}
	type inv = struct {
		args   []string
		routes map[string]reply
	}
	return map[string]inv{
		"domain register":                   {args: []string{"domain", "register", "new.com", "--years", "1"}},
		"domain renew":                      {args: []string{"domain", "renew", "example.com"}},
		"domain update":                     {args: []string{"domain", "update", "example.com", "--privacy=false"}},
		"domain lock":                       {args: []string{"domain", "lock", "off", "example.com"}},
		"domain lock, two":                  {args: []string{"domain", "lock", "off", "example.com", "example.net"}},
		"domain autorenew":                  {args: []string{"domain", "autorenew", "off", "example.com"}},
		"domain privacy":                    {args: []string{"domain", "privacy", "on", "example.com"}, routes: map[string]reply{"GET /core/v1/domains/example.com": unlocked}},
		"domain set-ns":                     {args: []string{"domain", "set-ns", "example.com", "--ns", "ns1.name.com,ns2.name.com"}},
		"domain contacts set":               {args: []string{"domain", "contacts", "set", "example.com", "--contacts-file", contactsFile}},
		"dns create":                        {args: []string{"dns", "create", "example.com", "--type", "A", "--host", "api", "--answer", "192.0.2.9"}},
		"dns create --if-not-exists":        {args: []string{"dns", "create", "example.com", "--type", "A", "--host", "api", "--answer", "192.0.2.9", "--if-not-exists"}},
		"dns create --if-not-exists, there": {args: []string{"dns", "create", "example.com", "--type", "A", "--host", "www", "--answer", "192.0.2.42", "--if-not-exists"}},
		"dns update":                        {args: []string{"dns", "update", "example.com", "42", "--ttl", "600"}},
		"dns update, no change":             {args: []string{"dns", "update", "example.com", "42", "--ttl", "300"}},
		"dns delete":                        {args: []string{"dns", "delete", "example.com", "42"}},
		"dns delete, two":                   {args: []string{"dns", "delete", "example.com", "42", "43"}},
		"dns import":                        {args: []string{"dns", "import", "example.com", "--file", recordsFile}},
		"dns sync":                          {args: []string{"dns", "sync", "example.com", "--file", recordsFile}},
		"dnssec create":                     {args: []string{"dnssec", "create", "example.com", "--algorithm", "8", "--digest-type", "2", "--key-tag", "12345", "--digest", strings.Repeat("ab", 32)}},
		"dnssec delete":                     {args: []string{"dnssec", "delete", "example.com", "abc123"}},
		"email create":                      {args: []string{"email", "create", "example.com", "info", "--to", "you@example.org"}},
		"email update":                      {args: []string{"email", "update", "example.com", "info", "--to", "new@example.org"}},
		"email delete":                      {args: []string{"email", "delete", "example.com", "info"}},
		"url create":                        {args: []string{"url", "create", "example.com", "--to", "https://example.org"}},
		"url update":                        {args: []string{"url", "update", "example.com", "7", "--to", "https://example.net"}},
		"url update, no change":             {args: []string{"url", "update", "example.com", "7", "--to", "https://example.org"}},
		"url delete":                        {args: []string{"url", "delete", "example.com", "7"}},
		"vanity-ns create":                  {args: []string{"vanity-ns", "create", "example.com", "--hostname", "ns1.example.com", "--ips", "192.0.2.1"}},
		"vanity-ns update":                  {args: []string{"vanity-ns", "update", "example.com", "ns1.example.com", "--ips", "192.0.2.2"}},
		"vanity-ns delete":                  {args: []string{"vanity-ns", "delete", "example.com", "ns1.example.com"}},
		"transfer create":                   {args: []string{"transfer", "create", "example.org", "--auth-code", "XYZ123"}},
		"transfer internal-in":              {args: []string{"transfer", "internal-in", "example.org", "--auth-code", "XYZ123"}},
		"transfer cancel":                   {args: []string{"transfer", "cancel", "example.org"}},
		"transfer cancel-outbound":          {args: []string{"transfer", "cancel-outbound", "example.com"}},
		"order refund":                      {args: []string{"order", "refund", "--order-id", "1", "--item-ids", "2"}},
		"contact resend":                    {args: []string{"contact", "resend", "9911"}},
		"contact verify":                    {args: []string{"contact", "verify", "9911"}},
		"config use":                        {args: []string{"config", "use", "work"}},
		"auth logout":                       {args: []string{"auth", "logout"}},
	}
}

// writeCommandsNotRun are the write commands writeInvocations leaves out.
var writeCommandsNotRun = map[string]string{
	"namecom api":        "prints what the API returned, whose keys are not known before it is sent",
	"namecom auth login": "reads a token from a terminal or stdin and checks it against the API",
}

// docKeys returns the keys of a write's JSON result: of the object, or of the
// items of a {"data": [...]} document.
func docKeys(t *testing.T, s string) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	var doc map[string]json.RawMessage
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, s)
	}
	if dec.More() {
		t.Fatalf("more than one JSON document:\n%s", s)
	}
	if raw, ok := doc["data"]; ok {
		var items []map[string]any
		if err := json.Unmarshal(raw, &items); err == nil {
			var keys []string
			for _, it := range items {
				for k := range it {
					if !slices.Contains(keys, k) {
						keys = append(keys, k)
					}
				}
			}
			return keys
		}
	}
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	return keys
}

// tsvKeys returns the keys a TSV result names: a list's header row, or the
// first cell of each field<TAB>value row.
func tsvKeys(s string, list bool) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if list {
		return strings.Split(lines[0], "\t")
	}
	keys := make([]string, len(lines))
	for i, l := range lines {
		keys[i], _, _ = strings.Cut(l, "\t")
	}
	return keys
}

// TestWriteResultKeys pins, for every write command, that the keys of what it
// prints once the write is made are one set whatever the format (#325):
//
//   - -o tsv names the keys -o json prints. A record-returning write printed
//     success/changed/message rows in TSV and the record in JSON, so
//     --fields, which picks JSON keys, could not pick what TSV had printed.
//   - --fields with exactly those keys prints the same TSV.
//   - the keys are the ones the command declares (cmdutil.SetResult), so a
//     mistyped --fields is a usage error before any request is sent. It was
//     found after the write and only warned about, the result printed as
//     JSON whatever -o said.
func TestWriteResultKeys(t *testing.T) {
	invs := writeInvocations(t)

	// Every write command is run here, or said why not.
	var walk func(c *cobra.Command)
	covered := map[string]bool{}
	for _, inv := range invs {
		covered[mustFind(t, inv.args).CommandPath()] = true
	}
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		if cmdutil.Kind(c) == cmdutil.KindWrite && !covered[c.CommandPath()] && writeCommandsNotRun[c.CommandPath()] == "" {
			t.Errorf("%s is a write that TestWriteResultKeys does not run; add it to writeInvocations", c.CommandPath())
		}
	}
	walk(rootCmd)

	for name, inv := range invs {
		t.Run(name, func(t *testing.T) {
			run := func(extra ...string) (string, string, int, []string) {
				t.Helper()
				// Each run has its own config: auth logout removes the profile.
				withConfig(t, loneProfile)
				resetFlags(t, inv.args)
				srv, requests := apiStub(t, inv.routes)
				args := append(append([]string{"--base-url", srv.URL, "--yes"}, extra...), inv.args...)
				stdout, stderr, code := runContract(t, args...)
				return stdout, stderr, code, requests()
			}
			declared := cmdutil.ResultKeys(mustFind(t, inv.args))

			jsonOut, stderr, code, _ := run("-o", "json")
			if code != 0 {
				t.Fatalf("-o json: exit %d; stderr:\n%s", code, stderr)
			}
			jkeys := docKeys(t, jsonOut)
			var head struct {
				Data json.RawMessage `json:"data"`
			}
			_ = json.Unmarshal([]byte(jsonOut), &head)
			list := head.Data != nil

			tsvOut, stderr, code, _ := run("-o", "tsv")
			if code != 0 {
				t.Fatalf("-o tsv: exit %d; stderr:\n%s", code, stderr)
			}
			tkeys := tsvKeys(tsvOut, list)
			for _, k := range jkeys {
				if !slices.Contains(tkeys, k) {
					t.Errorf("-o json has key %q that -o tsv does not:\n json: %s\n  tsv: %s", k, jsonOut, tsvOut)
				}
			}
			for _, k := range tkeys {
				if !slices.Contains(declared, k) {
					t.Errorf("-o tsv has key %q the command does not declare (%v)", k, declared)
				}
			}

			picked, stderr, code, _ := run("-o", "tsv", "--fields", strings.Join(tkeys, ","))
			if code != 0 || picked != tsvOut {
				t.Errorf("--fields %s -o tsv: exit %d, want 0 and the TSV without --fields\n got: %q\nwant: %q\nstderr: %s",
					strings.Join(tkeys, ","), code, picked, tsvOut, stderr)
			}

			_, stderr, code, sent := run("-o", "tsv", "--fields", "bogus")
			if code != 2 || len(sent) != 0 {
				t.Errorf("--fields bogus: exit %d after %d requests %v, want exit 2 before any; stderr:\n%s", code, len(sent), sent, stderr)
			}
		})
	}
}

// TestWriteNoOpTSV: a write that found nothing to do says changed false in
// TSV, as JSON does. dns update, url update and dns create --if-not-exists
// printed changed<TAB>true for a no-op (#325).
func TestWriteNoOpTSV(t *testing.T) {
	invs := writeInvocations(t)
	for _, name := range []string{"dns update, no change", "url update, no change", "dns create --if-not-exists, there"} {
		t.Run(name, func(t *testing.T) {
			inv := invs[name]
			withConfig(t, loneProfile)
			resetFlags(t, inv.args)
			srv, requests := apiStub(t, inv.routes)
			stdout, stderr, code := runContract(t, append([]string{"--base-url", srv.URL, "--yes", "-o", "tsv"}, inv.args...)...)
			if code != 0 || !slices.Contains(strings.Split(stdout, "\n"), "changed\tfalse") {
				t.Errorf("exit %d, want 0 and a changed<TAB>false row:\n%s\nstderr: %s", code, stdout, stderr)
			}
			for _, r := range requests() {
				if !strings.HasPrefix(r, "GET ") {
					t.Errorf("a no-op sent %s", r)
				}
			}
		})
	}
}
