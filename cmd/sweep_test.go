package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
)

// TestSweep_DegenerateResponses runs every command in the tree, through the
// real root, against a stub that answers every request with the same
// degenerate response: `{}`, `[]`, `null`, an object whose list fields are all
// null, lists holding a null element, lists holding one empty object, and a
// 204 with no body. Each runs in every output mode, and writes run both with
// and without --dry-run.
//
// The SDK turned many response fields into pointers, and every panic found in
// the migration came from a field the code assumed was there. The shape tests
// in each package pin realistic responses; this sweep pins the other side —
// that no response, however empty, crashes the CLI, leaks a decoder error, or
// leaves -o json / -o yaml with stdout that does not parse.
//
// The command list comes from the cobra tree, so a new command fails this
// test until it has an entry in sweepArgs.
//
// Each case runs in a child process (TestSweepChild): a panic inside a
// goroutine — status and domain list fan out with errgroup — cannot be
// recovered in-process and would take the whole test binary down with it.
func TestSweep_DegenerateResponses(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a process per case; skipped in -short")
	}
	leaves := sweepLeaves(rootCmd)
	for _, path := range leaves {
		if _, ok := sweepArgs[path]; !ok {
			t.Errorf("%q has no entry in sweepArgs: add plausible args for it so the sweep covers it", path)
		}
	}
	for path := range sweepArgs {
		if !slices.Contains(leaves, path) {
			t.Errorf("sweepArgs has an entry for %q, which is no longer a command", path)
		}
	}

	files := sweepFiles(t)

	for _, path := range leaves {
		entry, ok := sweepArgs[path]
		if !ok || entry.skip != "" {
			continue
		}
		args := entry.args(path, files)
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			hit := false
			for _, v := range sweepVariants {
				for _, mode := range []string{"table", "json", "yaml", "quiet"} {
					dryModes := []bool{false}
					if entry.write {
						dryModes = append(dryModes, true)
					}
					for _, dry := range dryModes {
						name := v.name + "/" + mode
						if dry {
							name += "/dry-run"
						}
						t.Run(name, func(t *testing.T) {
							res := runSweepChild(t, sweepCase{Variant: v.name, Mode: mode, Dry: dry, Args: args, Freeform: entry.freeform})
							if !dry && res.Requests > 0 {
								hit = true
							}
							if len(res.Problems) == 0 {
								return
							}
							if bug := knownSweepFailure(path, v.name, mode, dry, res.Problems); bug != "" {
								t.Skipf("known failure (%s): %s", bug, strings.Join(res.Problems, "; "))
							}
							for _, p := range res.Problems {
								t.Errorf("namecom %s [%s, dry-run=%v] against %s: %s",
									strings.Join(args, " "), mode, dry, v.name, p)
							}
						})
					}
				}
			}
			if !entry.noAPI && !hit {
				t.Errorf("%s never reached the stub: its args in sweepArgs are rejected before any request, so the sweep tests nothing", path)
			}
		})
	}
}

// sweepCase is one run, passed to the child process as JSON.
type sweepCase struct {
	Variant  string
	Mode     string
	Dry      bool
	Args     []string
	Freeform bool
}

// sweepOutcome is what the child reports back.
type sweepOutcome struct {
	Problems []string
	Requests int64
}

const (
	sweepCaseEnv   = "NAMECOM_SWEEP_CASE"
	sweepResultEnv = "NAMECOM_SWEEP_RESULT"
)

func runSweepChild(t *testing.T, c sweepCase) sweepOutcome {
	t.Helper()
	spec, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	resultPath := filepath.Join(t.TempDir(), "result.json")
	child := exec.Command(os.Args[0], "-test.run=^TestSweepChild$", "-test.count=1", "-test.v") //nolint:gosec // G204: re-runs this test binary, argv fixed
	child.Env = append(os.Environ(), sweepCaseEnv+"="+string(spec), sweepResultEnv+"="+resultPath)
	combined, runErr := child.CombinedOutput()

	data, readErr := os.ReadFile(resultPath)
	if readErr != nil {
		// No result: the child died before reporting, which is a panic the
		// in-process recover could not catch (one on another goroutine).
		out := string(combined)
		if i := strings.Index(out, "\npanic: "); i >= 0 {
			msg := strings.SplitN(out[i+1:], "\n", 2)[0]
			return sweepOutcome{Problems: []string{fmt.Sprintf("PANIC (goroutine) %s at %s", msg, panicSite(out[i:]))}, Requests: 1}
		}
		t.Fatalf("child process produced no result (exit: %v):\n%s", runErr, out)
	}
	var res sweepOutcome
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatalf("decoding child result: %v", err)
	}
	return res
}

// TestSweepChild runs one case for TestSweep_DegenerateResponses. It does
// nothing unless that test started it.
func TestSweepChild(t *testing.T) {
	spec := os.Getenv(sweepCaseEnv)
	if spec == "" {
		t.Skip("only runs as a child of TestSweep_DegenerateResponses")
	}
	var c sweepCase
	if err := json.Unmarshal([]byte(spec), &c); err != nil {
		t.Fatal(err)
	}
	var v sweepVariant
	for _, x := range sweepVariants {
		if x.name == c.Variant {
			v = x
		}
	}
	t.Cleanup(output.StubInteractive(false))
	res := runSweep(t, v, c.Mode, c.Dry, c.Args)
	t.Logf("err: %v\nrequests: %d\nstdout:\n%s\nstderr:\n%s", res.err, res.requests, res.stdout, res.stderr)
	out := sweepOutcome{Problems: res.problems(c.Mode, c.Freeform), Requests: res.requests}
	data, _ := json.Marshal(out)
	if err := os.WriteFile(os.Getenv(sweepResultEnv), data, 0o600); err != nil { //nolint:gosec // G703: path set by the parent test
		t.Fatal(err)
	}
}

// sweepEntry is how the sweep invokes one command.
type sweepEntry struct {
	argv     []string // after the command path; "{contacts}"/"{records}" are replaced by temp files
	write    bool     // also run with --dry-run
	noAPI    bool     // never makes a request (config, completion, …)
	freeform bool     // stdout is not a JSON/YAML document in -o json/yaml (scripts, raw passthrough)
	skip     string   // reason it cannot run here at all
}

func (e sweepEntry) args(path string, files map[string]string) []string {
	out := strings.Fields(path)
	for _, a := range e.argv {
		if f, ok := files[a]; ok {
			a = f
		}
		out = append(out, a)
	}
	return out
}

// sweepArgs has one entry per leaf command, keyed by its path below the root.
var sweepArgs = map[string]sweepEntry{
	"api":         {argv: []string{"GET", "/core/v1/hello"}, freeform: true},
	"auth login":  {skip: "interactive form only; never talks to the API"},
	"auth logout": {noAPI: true},
	"auth status": {},

	"completion bash":       {noAPI: true, freeform: true},
	"completion fish":       {noAPI: true, freeform: true},
	"completion powershell": {noAPI: true, freeform: true},
	"completion zsh":        {noAPI: true, freeform: true},

	"config list-profiles": {noAPI: true},
	"config show":          {noAPI: true},
	"config use":           {argv: []string{"work"}, noAPI: true},

	"contact resend":     {argv: []string{"123"}, write: true},
	"contact unverified": {},
	"contact verify":     {argv: []string{"123"}, write: true},

	"dns create": {argv: []string{"example.com", "--type", "A", "--host", "www", "--answer", "192.0.2.1"}, write: true},
	"dns delete": {argv: []string{"example.com", "42"}, write: true},
	"dns export": {argv: []string{"example.com"}},
	"dns import": {argv: []string{"example.com", "--file", "{records}"}, write: true},
	"dns list":   {argv: []string{"example.com"}},
	"dns update": {argv: []string{"example.com", "42", "--answer", "192.0.2.2"}, write: true},

	"dnssec create": {argv: []string{"example.com", "--algorithm", "8", "--digest", "ABCDEF0123", "--digest-type", "2", "--key-tag", "12345"}, write: true},
	"dnssec delete": {argv: []string{"example.com", "ABCDEF0123"}, write: true},
	"dnssec get":    {argv: []string{"example.com", "ABCDEF0123"}},
	"dnssec list":   {argv: []string{"example.com"}},

	"domain auth-code":    {argv: []string{"example.com"}},
	"domain autorenew":    {argv: []string{"on", "example.com"}, write: true},
	"domain check":        {argv: []string{"example.com"}},
	"domain claims":       {argv: []string{"example.com"}},
	"domain contacts get": {argv: []string{"example.com"}},
	"domain contacts set": {argv: []string{"example.com", "--from-file", "{contacts}"}, write: true},
	"domain get":          {argv: []string{"example.com"}},
	"domain list":         {},
	"domain lock":         {argv: []string{"on", "example.com"}, write: true},
	"domain pricing":      {argv: []string{"example.com"}},
	"domain privacy":      {argv: []string{"on", "example.com"}, write: true},
	"domain register":     {argv: []string{"example.com"}, write: true},
	"domain renew":        {argv: []string{"example.com"}, write: true},
	"domain requirements": {argv: []string{"com"}},
	"domain search":       {argv: []string{"example"}},
	"domain set-ns":       {argv: []string{"example.com", "--ns", "ns1.example.net,ns2.example.net"}, write: true},
	"domain update":       {argv: []string{"example.com", "--autorenew"}, write: true},

	"email create": {argv: []string{"example.com", "info", "--to", "a@example.net"}, write: true},
	"email delete": {argv: []string{"example.com", "info"}, write: true},
	"email get":    {argv: []string{"example.com", "info"}},
	"email list":   {argv: []string{"example.com"}},
	"email update": {argv: []string{"example.com", "info", "--to", "b@example.net"}, write: true},

	"open": {skip: "launches a browser; never talks to the API"},

	"order get":    {argv: []string{"123"}},
	"order list":   {},
	"order refund": {argv: []string{"--order-id", "123", "--item-ids", "456"}, write: true},

	"status": {},

	"transfer cancel":          {argv: []string{"example.com"}, write: true},
	"transfer cancel-outbound": {argv: []string{"example.com"}, write: true},
	"transfer create":          {argv: []string{"example.com", "--auth-code", "abc123"}, write: true},
	"transfer eligibility":     {argv: []string{"example.com"}},
	"transfer get":             {argv: []string{"example.com"}},
	"transfer internal-in":     {argv: []string{"example.com", "--auth-code", "abc123"}, write: true},
	"transfer list":            {},

	"url create": {argv: []string{"example.com", "--host", "www", "--to", "https://example.org"}, write: true},
	"url delete": {argv: []string{"example.com", "42"}, write: true},
	"url get":    {argv: []string{"example.com", "42"}},
	"url list":   {argv: []string{"example.com"}},
	"url update": {argv: []string{"example.com", "42", "--to", "https://example.org/b"}, write: true},

	"vanity-ns create": {argv: []string{"example.com", "--hostname", "ns1.example.com", "--ips", "192.0.2.1"}, write: true},
	"vanity-ns delete": {argv: []string{"example.com", "ns1.example.com"}, write: true},
	"vanity-ns get":    {argv: []string{"example.com", "ns1.example.com"}},
	"vanity-ns list":   {argv: []string{"example.com"}},
	"vanity-ns update": {argv: []string{"example.com", "ns1.example.com", "--ips", "192.0.2.2"}, write: true},

	"version": {noAPI: true},
}

// The bugs the sweep found, not yet fixed. Each row of knownSweepFailures
// names one of them.
const (
	// A 200 whose body is `null` decodes to a nil response with a nil error,
	// and the command dereferences the response. Every site is a different
	// line, so each command has its own row.
	bugNullBody = "null body: nil response dereferenced"
	// A list holding a null element (`"records":[null]`) reaches a table row
	// builder or a --quiet loop that dereferences each element unchecked.
	bugNullElement = "null list element dereferenced"
	// A body of the wrong JSON shape (`[]` where an object is expected)
	// surfaces the decoder's error, Go type names included: `json: cannot
	// unmarshal array into Go value of type api.unmarshaler`. One place
	// (error normalization) fixes every command, so one row covers them.
	bugWrongShape = "wrong-shape body leaks the JSON decoder error"
	// An empty body (here, a 204) where a response is expected surfaces the
	// SDK's `expected a **api.Record response, but the server responded with
	// nothing`. Also one fix for every command.
	bugEmptyBody = "empty body leaks the SDK's Go type name"
)

// Problem kinds a knownSweepFailures row can excuse.
const (
	panicked = "PANIC"
	rawError = "raw error leaked"
)

// knownSweepFailures lists failures the sweep has found and that are not yet
// fixed, so the branch stays green while each is tracked. An empty cmd or
// modes matches anything; modes lists output modes, with "/dry" for the
// --dry-run run. A row excuses only problems of its kind: a panic in a case
// covered by a raw-error row still fails. Remove a row once its bug is
// fixed: the case then runs, and fails if the fix missed it.
var knownSweepFailures = []struct {
	bug, cmd, variant, modes, kind string
}{
	{bugWrongShape, "", "empty-array", "", rawError},
	{bugEmptyBody, "", "204-no-body", "", rawError},
	{bugNullBody, "contact resend", "null", "json,quiet,table,yaml", panicked},
	{bugNullBody, "contact unverified", "null", "", panicked},
	{bugNullElement, "contact unverified", "null-elements", "quiet,table", panicked},
	{bugNullBody, "dns create", "null", "table", panicked},
	{bugNullBody, "dns export", "null", "", panicked},
	{bugNullBody, "dns list", "null", "", panicked},
	{bugNullElement, "dns list", "null-elements", "quiet,table", panicked},
	{bugNullBody, "dns update", "null", "", panicked},
	{bugNullBody, "dnssec create", "null", "table", panicked},
	{bugNullBody, "dnssec get", "null", "quiet,table", panicked},
	{bugNullBody, "dnssec list", "null", "", panicked},
	{bugNullElement, "dnssec list", "null-elements", "quiet,table", panicked},
	{bugNullBody, "domain auth-code", "null", "quiet,table", panicked},
	{bugNullBody, "domain check", "null", "", panicked},
	{bugNullElement, "domain check", "null-elements", "", panicked},
	{bugNullBody, "domain claims", "null", "", panicked},
	{bugNullBody, "domain contacts get", "null", "", panicked},
	{bugNullBody, "domain get", "null", "quiet,table", panicked},
	{bugNullBody, "domain list", "null", "", panicked},
	{bugNullElement, "domain list", "null-elements", "quiet,table", panicked},
	{bugNullBody, "domain pricing", "null", "table", panicked},
	{bugNullBody, "domain register", "null", "", panicked},
	{bugNullElement, "domain register", "null-elements", "", panicked},
	{bugNullBody, "domain renew", "null", "", panicked},
	{bugNullBody, "domain requirements", "null", "quiet,table", panicked},
	{bugNullBody, "domain search", "null", "", panicked},
	{bugNullElement, "domain search", "null-elements", "quiet,table", panicked},
	{bugNullBody, "email get", "null", "table", panicked},
	{bugNullBody, "email list", "null", "", panicked},
	{bugNullElement, "email list", "null-elements", "quiet,table", panicked},
	{bugNullBody, "order get", "null", "table", panicked},
	{bugNullElement, "order get", "null-elements", "table", panicked},
	{bugNullBody, "order list", "null", "", panicked},
	{bugNullElement, "order list", "null-elements", "quiet,table", panicked},
	{bugNullBody, "order refund", "null", "json,quiet,table,yaml", panicked},
	{bugNullBody, "status", "null", "", panicked},
	{bugNullElement, "status", "null-elements", "", panicked},
	{bugNullBody, "transfer cancel-outbound", "null", "table", panicked},
	{bugNullBody, "transfer create", "null", "", panicked},
	{bugNullBody, "transfer eligibility", "null", "table", panicked},
	{bugNullBody, "transfer get", "null", "quiet,table", panicked},
	{bugNullBody, "transfer list", "null", "", panicked},
	{bugNullElement, "transfer list", "null-elements", "quiet,table", panicked},
	{bugNullBody, "url create", "null", "table", panicked},
	{bugNullBody, "url get", "null", "quiet,table", panicked},
	{bugNullBody, "url list", "null", "", panicked},
	{bugNullElement, "url list", "null-elements", "quiet,table", panicked},
	{bugNullBody, "url update", "null", "", panicked},
	{bugNullBody, "vanity-ns get", "null", "quiet,table", panicked},
	{bugNullBody, "vanity-ns list", "null", "", panicked},
	{bugNullElement, "vanity-ns list", "null-elements", "quiet,table", panicked},
}

// knownSweepFailure returns the bug that excuses every one of problems, or ""
// when any of them is not excused.
func knownSweepFailure(cmd, variant, mode string, dry bool, problems []string) string {
	key := mode
	if dry {
		key += "/dry"
	}
	bug := ""
	for _, p := range problems {
		matched := false
		for _, k := range knownSweepFailures {
			if (k.cmd == "" || k.cmd == cmd) && k.variant == variant &&
				(k.modes == "" || slices.Contains(strings.Split(k.modes, ","), key)) &&
				strings.HasPrefix(p, k.kind) {
				matched, bug = true, k.bug
				break
			}
		}
		if !matched {
			return ""
		}
	}
	return bug
}

// sweepVariant is one degenerate response the stub gives to every request.
type sweepVariant struct {
	name   string
	status int
	body   string
}

// sweepListKeys is every list-valued field in a Core SDK response.
var sweepListKeys = []string{
	"domains", "records", "emailForwarding", "urlForwarding", "transfers", "orders",
	"orderItems", "results", "pricing", "claims", "unverifiedContacts",
	"vanityNameservers", "dnssec", "subscriptions",
}

// sweepScalarListKeys are list fields of non-pointer elements.
var sweepScalarListKeys = []string{
	"nameservers", "ips", "locks", "domainNames", "registryStatuses", "statuses",
	"required", "tldFilter", "allowedRegistrationYears", "orderItemIds", "claimsCheckRequired",
}

func sweepListBody(objElem string, includeScalars bool) string {
	m := map[string]json.RawMessage{}
	for _, k := range sweepListKeys {
		m[k] = json.RawMessage(objElem)
	}
	if includeScalars {
		for _, k := range sweepScalarListKeys {
			m[k] = json.RawMessage("null")
		}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

var sweepVariants = []sweepVariant{
	{"empty-object", http.StatusOK, `{}`},
	{"empty-array", http.StatusOK, `[]`},
	{"null", http.StatusOK, `null`},
	{"null-lists", http.StatusOK, sweepListBody("null", true)},
	{"null-elements", http.StatusOK, sweepListBody("[null]", false)},
	{"minimal-elements", http.StatusOK, sweepListBody("[{}]", false)},
	{"204-no-body", http.StatusNoContent, ``},
}

type sweepResult struct {
	err      error
	panicked any
	stack    string
	stdout   string
	stderr   string
	requests int64
}

// rawErrorPattern matches error text that is a Go or decoder internal, not a
// message written for the user.
var rawErrorPattern = regexp.MustCompile(`unexpected end of JSON input|invalid character|cannot unmarshal|json: |yaml: |^EOF$|: EOF$|runtime error|nil pointer|unexpected EOF|\*[a-z]+\.[A-Z][A-Za-z]+ response`)

func (r sweepResult) problems(mode string, freeform bool) []string {
	var out []string
	if r.panicked != nil {
		return []string{fmt.Sprintf("PANIC %v at %s", r.panicked, panicSite(r.stack))}
	}
	if r.err != nil {
		code := exitCode(r.err)
		_, isAPI := errors.AsType[*api.APIError](r.err)
		if code == 1 && !isAPI && rawErrorPattern.MatchString(r.err.Error()) {
			out = append(out, fmt.Sprintf("raw error leaked (exit %d): %q", code, r.err.Error()))
		}
	}
	if freeform {
		return out
	}
	switch mode {
	case "json":
		if r.err == nil && strings.TrimSpace(r.stdout) == "" {
			out = append(out, "succeeded with empty stdout in -o json")
		} else if n, err := countJSONDocs(r.stdout); err != nil {
			out = append(out, fmt.Sprintf("stdout is not valid JSON: %v\n%s", err, r.stdout))
		} else if n > 1 {
			out = append(out, fmt.Sprintf("stdout holds %d JSON documents, want one\n%s", n, r.stdout))
		}
	case "yaml":
		if r.err == nil && strings.TrimSpace(r.stdout) == "" {
			out = append(out, "succeeded with empty stdout in -o yaml")
		} else if err := checkYAML(r.stdout); err != nil {
			out = append(out, fmt.Sprintf("stdout is not valid YAML: %v\n%s", err, r.stdout))
		}
	}
	return out
}

func countJSONDocs(s string) (int, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	n := 0
	for {
		var v any
		err := dec.Decode(&v)
		if errors.Is(err, io.EOF) {
			return n, nil
		}
		if err != nil {
			return n, err
		}
		n++
	}
}

func checkYAML(s string) error {
	dec := yaml.NewDecoder(strings.NewReader(s))
	for {
		var v any
		err := dec.Decode(&v)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// panicSite returns the first frame of this module's non-test code in a
// panic stack — where the dereference happened.
func panicSite(stack string) string {
	lines := strings.Split(stack, "\n")
	for i, l := range lines {
		l = strings.TrimSpace(l)
		if strings.Contains(l, "/namecom-cli/") && strings.Contains(l, ".go:") &&
			!strings.Contains(l, "_test.go") && i > 0 {
			if f := strings.Fields(l); len(f) > 0 {
				file := f[0]
				for _, dir := range []string{"/cmd/", "/internal/"} {
					if j := strings.LastIndex(file, dir); j >= 0 {
						file = file[j+1:]
						break
					}
				}
				fn := strings.TrimSpace(lines[i-1])
				if j := strings.LastIndex(fn, "("); j > 0 {
					fn = fn[:j]
				}
				return file + " in " + fn[strings.LastIndex(fn, "/")+1:]
			}
		}
	}
	return "unknown site\n" + stack
}

func runSweep(t *testing.T, v sweepVariant, mode string, dry bool, args []string) (res sweepResult) {
	t.Helper()
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		if v.status != http.StatusNoContent {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(v.status)
		_, _ = w.Write([]byte(v.body))
	}))
	defer srv.Close()
	withConfig(t, loneProfile)

	dir := t.TempDir()
	stdoutF, _ := os.Create(filepath.Join(dir, "stdout"))
	stderrF, _ := os.Create(filepath.Join(dir, "stderr"))
	stdinF, _ := os.Open(os.DevNull)
	oldOut, oldErr, oldIn := os.Stdout, os.Stderr, os.Stdin
	os.Stdout, os.Stderr, os.Stdin = stdoutF, stderrF, stdinF

	prev := gf
	full := []string{"--base-url", srv.URL, "--yes", "--color", "never", "--timeout", "5s"}
	if mode == "quiet" {
		full = append(full, "-q")
	} else {
		full = append(full, "-o", mode)
	}
	if dry {
		full = append(full, "--dry-run")
	}
	full = append(full, args...)

	defer func() {
		if p := recover(); p != nil {
			res.panicked = p
			res.stack = string(debug.Stack())
		}
		os.Stdout, os.Stderr, os.Stdin = oldOut, oldErr, oldIn
		_ = stdinF.Close()
		_ = stdoutF.Close()
		_ = stderrF.Close()
		gf = prev
		rootCmd.SetArgs(nil)
		resetFlags(rootCmd)
		o, _ := os.ReadFile(stdoutF.Name())
		e, _ := os.ReadFile(stderrF.Name())
		res.stdout, res.stderr = string(o), string(e)
		res.requests = n.Load()
	}()

	rootCmd.SetArgs(full)
	err := cmdutil.ClassifyCobraUsage(rootCmd.ExecuteContext(context.Background()))
	if err != nil {
		err = normalizeError(err)
	}
	res.err = err
	return res
}

// resetFlags returns every flag in the tree to its default, so one run's
// flags do not leak into the next — cobra keeps flag values between Execute
// calls on the same command tree.
func resetFlags(c *cobra.Command) {
	reset := func(f *pflag.Flag) {
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			_ = sv.Replace(nil)
		} else {
			_ = f.Value.Set(f.DefValue)
		}
		f.Changed = false
	}
	c.PersistentFlags().VisitAll(reset)
	c.Flags().VisitAll(reset)
	for _, s := range c.Commands() {
		resetFlags(s)
	}
}

// sweepLeaves returns the path, below the root, of every runnable leaf command.
func sweepLeaves(root *cobra.Command) []string {
	var out []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.HasSubCommands() {
			for _, s := range c.Commands() {
				walk(s)
			}
			return
		}
		if c.Name() == "help" || !c.Runnable() {
			return
		}
		out = append(out, strings.TrimPrefix(c.CommandPath(), root.Name()+" "))
	}
	walk(root)
	sort.Strings(out)
	return out
}

// sweepFiles writes the input files some commands read.
func sweepFiles(t *testing.T) map[string]string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"{records}":  `[{"host":"www","type":"A","answer":"192.0.2.1","ttl":300}]`,
		"{contacts}": `{"registrant":{"firstName":"Ada","lastName":"Lovelace","address1":"1 Main St","city":"Springfield","state":"CA","zip":"90000","country":"US","email":"ada@example.net","phone":"+1.5555550100"}}`,
	}
	paths := map[string]string{}
	for k, body := range files {
		p := filepath.Join(dir, strings.Trim(k, "{}")+".json")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", p, err)
		}
		paths[k] = p
	}
	return paths
}
