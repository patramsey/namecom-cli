// Package apicmd implements the `namecom api` raw HTTP passthrough command.
package apicmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// Cmd is the `namecom api` command.
var Cmd = &cobra.Command{
	Use:   "api [METHOD] <path>",
	Short: "Make a raw API request",
	Long: `Make a raw HTTP request to the name.com API. Auth, rate limiting, and retries are applied automatically.

The method is the first argument, or -X/--method; either way any case will
do. It may be left out: it is GET, or POST when --data, --input, -f or -F
gives the request a body. With --paginate it is always GET.

The body is one of:
  --data <json>     the body as given; --data - reads it from stdin
  --input <file>    the body read from a file; --input - reads stdin
  -f key=value      a JSON body built from fields; the value is a string
  -F key=value      the same, but true, false, null and numbers keep their
                    JSON type, and @file (or @- for stdin) is the contents
                    of that file as a string
A key may nest, as -f 'contact[firstName]=Ada', and a trailing [] appends to
a list, as -f 'nameservers[]=ns1.example.com'. For GET and HEAD, -f and -F
are query parameters instead.

Without any of these, a method other than GET or HEAD reads its body from
stdin when stdin is a pipe or a file; an empty stdin sends no body.
--data '' sends no body without reading stdin.

--paginate follows nextPage until the last page and prints one document: each
list in it is every page's items end to end, and nextPage and lastPage are
gone. The global --jq filters that merged document.

--include prints the response status line and headers before the body; with
--paginate, those of each page, then the merged body. --jq and --fields
filter the body alone.

Any method other than GET or HEAD is a write, and is confirmed as other
writes are: a question in a terminal, and --yes when not in one. A POST
inferred from a body says so, in the question and in a warning: to send -f
fields as a GET's query, pass -X GET. With --dry-run, a write is printed —
method, path, and body — instead of sent. GET and HEAD still run.`,
	Example: `  namecom api /core/v1/domains
  namecom api /core/v1/domains -X GET -f perPage=1000
  namecom api /core/v1/domains --paginate --jq '.domains[].domainName'
  namecom api GET /core/v1/domains/example.com --include
  namecom api /core/v1/domains --include --jq '.totalCount'
  namecom api POST /core/v1/domains/example.com/records -f host=@ -f type=A -f answer=1.2.3.4 -F ttl=300
  namecom api PUT /core/v1/domains/example.com/records/123 --input record.json

  # In a script, skip the confirmation:
  echo '{"host":"www","type":"CNAME","answer":"example.com.","ttl":300}' | namecom api POST /core/v1/domains/example.com/records --yes
  namecom api -X PATCH /core/v1/domains/example.com -F autorenewEnabled=true --dry-run
  namecom api DELETE /core/v1/domains/example.com/records/123 --dry-run`,
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) > 2 {
			return cmdutil.ExactArgs(2)(cmd, args)
		}
		return cmdutil.MinimumNArgs(1)(cmd, args)
	},
	// The method, then nothing: a path is not a file (#187).
	ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return allowedMethods, cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	},
	RunE: runAPI,
}

// allowedMethods are the methods `namecom api` sends, matched case-insensitively.
var allowedMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete, http.MethodOptions,
}

var (
	apiBody     string
	apiHeaders  []string
	apiFields   []string
	apiTyped    []string
	apiInput    string
	apiMethod   string
	apiInclude  bool
	apiPaginate bool
)

func init() {
	f := Cmd.Flags()
	f.StringVarP(&apiMethod, "method", "X", "", "the HTTP method, in place of the first argument (default GET, or POST with a body)")
	f.StringVar(&apiBody, "data", "", "request body (JSON); use '-' to read from stdin")
	f.StringVar(&apiInput, "input", "", "read the request body from a file; use '-' for stdin")
	f.StringArrayVarP(&apiFields, "raw-field", "f", nil, "add a string parameter, key=value: the JSON body, or the query of a GET")
	f.StringArrayVarP(&apiTyped, "field", "F", nil, "add a typed parameter, key=value: true, false, null and numbers keep their type; @file reads a file")
	f.StringArrayVar(&apiHeaders, "header", nil, "additional headers: 'Name: Value'")
	f.BoolVarP(&apiInclude, "include", "i", false, "print the response status line and headers before the body")
	f.BoolVar(&apiPaginate, "paginate", false, "follow nextPage and print every page as one document (GET only)")
	// Any method but GET or HEAD goes through RunWrite.
	cmdutil.MarkWrite(Cmd)
	cmdutil.CompleteFlagValues(Cmd, "method", allowedMethods)
}

// methodAndPath reads the method and path from args and -X. The method may
// be left out: then the one argument is the path, and hasBody picks the
// method.
func methodAndPath(cmd *cobra.Command, args []string, hasBody bool) (method, path string, err error) {
	// Checked before anything else, including --dry-run. nginx answers an
	// unknown method with 403, which exited 3 with an `auth login` hint for
	// what was a typo.
	flagMethod, err := checkMethod(apiMethod, "-X ")
	if err != nil {
		return "", "", err
	}
	if len(args) == 1 {
		if slices.Contains(allowedMethods, strings.ToUpper(args[0])) {
			return "", "", cmdutil.NewUsageError(fmt.Errorf("path is required — try: %s", cmd.UseLine()))
		}
		switch {
		case flagMethod != "":
			return flagMethod, args[0], nil
		case hasBody && !apiPaginate:
			return http.MethodPost, args[0], nil
		}
		return http.MethodGet, args[0], nil
	}
	if method, err = checkMethod(args[0], ""); err != nil {
		return "", "", err
	}
	if flagMethod != "" && flagMethod != method {
		return "", "", cmdutil.NewUsageError(fmt.Errorf("the method is given twice, as %s and as -X %s; use one", args[0], apiMethod))
	}
	return method, args[1], nil
}

// inferredFrom names the flag that made method POST when neither an argument
// nor -X gave a method, and is "" when the method was given. nargs is the
// number of arguments: with two, the first is the method.
func inferredFrom(nargs int, method string, dataSet bool) string {
	if nargs != 1 || apiMethod != "" || method != http.MethodPost {
		return ""
	}
	switch {
	case len(apiFields) > 0:
		return "-f"
	case len(apiTyped) > 0:
		return "-F"
	case dataSet:
		return "--data"
	}
	return "--input"
}

// checkMethod returns m in upper case, or a usage error when it is not a
// method `namecom api` sends. An empty m is no method, and returned as is.
// how says where m came from, for the message.
func checkMethod(m, how string) (string, error) {
	method := strings.ToUpper(m)
	if m == "" || slices.Contains(allowedMethods, method) {
		return method, nil
	}
	return "", cmdutil.NewUsageError(fmt.Errorf("unknown HTTP method %s%q: must be one of %s",
		how, m, strings.Join(allowedMethods, ", ")))
}

// checkFlags rejects flags that contradict each other or the method, before
// anything is read or sent.
func checkFlags(method string, dataSet bool) error {
	hasFields := len(apiFields)+len(apiTyped) > 0
	switch {
	case apiInput != "" && dataSet:
		return cmdutil.NewUsageError(errors.New("--input and --data both give the body; use one"))
	case apiInput != "" && hasFields:
		return cmdutil.NewUsageError(errors.New("--input cannot be combined with -f or -F: both give the body"))
	case dataSet && hasFields:
		return cmdutil.NewUsageError(errors.New("--data cannot be combined with -f or -F: both give the body"))
	case apiPaginate && method != http.MethodGet:
		return cmdutil.NewUsageError(fmt.Errorf("--paginate follows the pages of a GET, not a %s", method))
	}
	return nil
}

func runAPI(cmd *cobra.Command, args []string) error {
	dataSet := apiBody != "" || cmd.Flags().Changed("data")
	hasBody := dataSet || apiInput != "" || len(apiFields)+len(apiTyped) > 0
	method, rawPath, err := methodAndPath(cmd, args, hasBody)
	if err != nil {
		return err
	}
	if err := checkFlags(method, dataSet); err != nil {
		return err
	}
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	base := client.BaseURL()
	u, err := buildAPIURL(base, rawPath)
	if err != nil {
		return err
	}

	fields, err := parseFields(apiFields, apiTyped)
	if err != nil {
		return err
	}
	isRead := method == http.MethodGet || method == http.MethodHead

	// Read the body once, up front: stdin cannot be read twice, and --dry-run
	// must preview the bytes the request would carry. The retry transport
	// buffers the body for replay anyway, so streaming it saved nothing.
	//
	// Without a body flag, a write reads a piped stdin, as the help's own
	// example does (#231). Only a pipe or a file is read: a terminal would sit
	// waiting for typing, and the null device is what `</dev/null` in CI
	// gives. An empty read is no body, the same as no --data, rather than an
	// empty body labelled application/json.
	var body []byte
	switch {
	case len(fields) > 0 && isRead:
		// A read's fields are its query, after any the path has.
		parsed, err := url.Parse(u)
		if err != nil {
			return fmt.Errorf("building URL: %w", err)
		}
		q := fieldsQuery(fields).Encode()
		if parsed.RawQuery != "" {
			q = parsed.RawQuery + "&" + q
		}
		parsed.RawQuery = q
		u = parsed.String()
	case len(fields) > 0:
		if body, err = fieldsBody(fields); err != nil {
			return err
		}
	case apiInput == "-", apiBody == "-",
		!hasBody && !isRead && stdinIsPiped():
		if body, err = io.ReadAll(os.Stdin); err != nil {
			return fmt.Errorf("reading request body from stdin: %w", err)
		}
	case apiInput != "":
		if body, err = os.ReadFile(apiInput); err != nil { //nolint:gosec // G304: --input names the file to read; that is the flag's purpose
			return fmt.Errorf("reading request body: %w", err)
		}
	default:
		body = []byte(apiBody)
	}

	headers := make([][2]string, 0, len(apiHeaders))
	for _, h := range apiHeaders {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid header %q: expected 'Name: Value'", h)
		}
		headers = append(headers, [2]string{strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])})
	}

	// do sends one request to target and returns the body of a 2xx reply.
	do := func(ctx context.Context, target string, body []byte) ([]byte, error) {
		var bodyReader io.Reader
		if len(body) > 0 {
			bodyReader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, target, bodyReader)
		if err != nil {
			return nil, fmt.Errorf("building request: %w", err)
		}
		if bodyReader != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for _, h := range headers {
			req.Header.Set(h[0], h[1])
		}

		// HTTPClient() supplies rate limiting and retries via its transport, but auth
		// headers come from the generated client's request editor, which only runs
		// inside generated endpoint methods. Apply them explicitly — without this
		// every raw request goes out unauthenticated. Prepare leaves any header set
		// above (including --header overrides) untouched.
		if err := client.Prepare(req); err != nil {
			return nil, fmt.Errorf("preparing request: %w", err)
		}
		resp, err := client.HTTPClient().Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("reading response: %w", err)
		}
		// The head goes around --jq and --fields, which filter the body
		// alone: it is not JSON (#269).
		if apiInclude {
			writeHead(out.Unfiltered(), resp)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			// Return the normalized error type so root.go's exit-code mapping and
			// UserHint apply here too. A plain fmt.Errorf collapsed every failure to
			// exit 1, hiding the documented auth/rate-limit codes from scripts.
			apiErr := api.ErrorFromResponse(resp.StatusCode, respBody)
			// In JSON and YAML modes the error envelope is all that goes to
			// stderr, so it stays one parseable document; the body rides in
			// it as details. Printing it here as well put three things there.
			if out.Format == output.FormatJSON || out.Format == output.FormatYAML {
				apiErr.Body = respBody
				return nil, apiErr
			}
			fmt.Fprintf(out.EWriter, "HTTP %d\n", resp.StatusCode)
			_, _ = out.EWriter.Write(respBody)
			fmt.Fprintln(out.EWriter)
			return nil, apiErr
		}
		return respBody, nil
	}

	send := func(ctx context.Context, body []byte) error {
		respBody, err := do(ctx, u, body)
		if err != nil {
			return err
		}
		fmt.Fprintf(out.Writer, "%s\n", respBody)
		return nil
	}

	if apiPaginate {
		return paginate(cmd.Context(), u, body, do, out.Writer)
	}
	// Reads still run under --dry-run, as the flag's help promises. Every
	// other method is previewed: a raw passthrough cannot tell whether a POST
	// changes anything (checkAvailability does not; POST /domains buys a
	// domain), so it treats them all as writes.
	if isRead {
		return send(cmd.Context(), body)
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return fmt.Errorf("building URL: %w", err)
	}
	var payload any = cmdutil.NoBody{}
	if len(body) > 0 {
		payload = rawBody(body)
	}
	// A write confirms like every other write: a question in a terminal,
	// --yes off one (#282). It used to go straight out, so adding -f to a
	// list's path to page it sent an unconfirmed POST to that path — for
	// /core/v1/domains, a registration. The question says when the method
	// was inferred, since that is the surprise; so does a warning, which a
	// dry run and --yes print too.
	label := method
	if from := inferredFrom(len(args), method, dataSet); from != "" {
		label += " (inferred from " + from + ")"
		hint := "; pass -X POST to say so"
		if from == "-f" || from == "-F" {
			hint = "; pass -X GET to send the fields as query parameters instead"
		}
		out.Warn(label + hint)
	}
	_, err = cmdutil.RunWrite(cmd, cmdutil.Write[any]{
		Method: method, Path: parsed.RequestURI(), Body: payload,
		Prompt: fmt.Sprintf("Send %s %s?", label, parsed.RequestURI()),
	}, func(ctx context.Context, b any) error {
		rb, _ := b.(rawBody) // nil for NoBody
		return send(ctx, rb)
	})
	return err
}

// paginate GETs target and each nextPage after it, and prints the pages
// merged into one document. A reply that is not a JSON object has no pages,
// and is printed as it came. Nothing is printed when a page fails: half a
// list would read as all of it.
func paginate(ctx context.Context, target string, body []byte,
	do func(context.Context, string, []byte) ([]byte, error), w io.Writer) error {
	var merged pages
	seen := map[int]bool{startPage(target): true}
	for n := 1; ; n++ {
		page, err := do(ctx, target, body)
		if err != nil {
			return err
		}
		next, err := merged.add(page)
		if errors.Is(err, errNotObject) && n == 1 {
			_, err = fmt.Fprintf(w, "%s\n", page)
			return err
		}
		if err != nil {
			return fmt.Errorf("--paginate: response %d: %w", n, err)
		}
		if next == 0 {
			break
		}
		// A page already fetched would loop for ever.
		if seen[next] {
			return fmt.Errorf("--paginate: the API gave page %d as the next page again; stopping", next)
		}
		seen[next] = true
		if target, err = withPage(target, next); err != nil {
			return fmt.Errorf("building URL: %w", err)
		}
	}
	doc, err := merged.bytes()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", doc)
	return err
}

// writeHead prints resp's status line and headers, sorted by name, then a
// blank line, as `curl -i` and `gh api -i` do. They are the response's
// headers: the request's Authorization is never among them.
func writeHead(w io.Writer, resp *http.Response) {
	fmt.Fprintf(w, "%s %s\n", resp.Proto, resp.Status)
	names := make([]string, 0, len(resp.Header))
	for name := range resp.Header {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, v := range resp.Header[name] {
			fmt.Fprintf(w, "%s: %s\n", name, v)
		}
	}
	fmt.Fprintln(w)
}

// stdinIsPiped reports whether stdin is a pipe or a regular file: input that
// was handed to the command and ends. A terminal or the null device is a
// character device, and is not read.
func stdinIsPiped() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeNamedPipe != 0 || fi.Mode().IsRegular()
}

// rawBody is a --data body as --dry-run prints it: parsed and re-indented when
// it is JSON, and as a JSON string otherwise, since it is sent verbatim either
// way.
type rawBody []byte

// MarshalJSON implements json.Marshaler.
func (b rawBody) MarshalJSON() ([]byte, error) {
	if json.Valid(b) {
		return b, nil
	}
	return json.Marshal(string(b))
}

// buildAPIURL joins a user-supplied path onto the API base URL.
//
// This is a security boundary, which is why it is a separate function: the path
// comes straight from argv and the resulting request carries the account's
// credential. url.JoinPath escapes the path and cleans traversal, so a
// protocol-relative "//evil.example", an absolute URL, or "../.." cannot
// retarget the request. (url.ResolveReference, the obvious alternative, does
// NOT — it honours all three.)
//
// It is factored out so the property can be tested on the constructed URL
// rather than by observing network behaviour. Asserting "the attacker's server
// wasn't contacted" is not a real check: if a hostile path DOES retarget the
// request, the local test server simply never runs and the assertions pass
// vacuously — and even a failed connection means the credential already left.
//
// The query string is split off before joining and reattached verbatim. JoinPath
// treats its whole argument as a path, so it escaped "?" to "%3F" and every
// query parameter became part of a path the API does not have — a 403 for any
// filter, sort, or page. Reattaching it to the already-checked URL cannot move
// the request: RawQuery never changes the host.
func buildAPIURL(base, rawPath string) (string, error) {
	pathPart, query, hasQuery := strings.Cut(rawPath, "?")
	u, err := url.JoinPath(base, pathPart)
	if err != nil {
		return "", fmt.Errorf("building URL: %w", err)
	}
	baseParsed, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("building URL: %w", err)
	}
	joined, err := url.Parse(u)
	if err != nil {
		return "", fmt.Errorf("building URL: %w", err)
	}
	// Belt and braces: refuse anything that moved off the configured host.
	if joined.Host != baseParsed.Host || joined.Scheme != baseParsed.Scheme {
		return "", fmt.Errorf("path %q would send the request to %s://%s, not %s — refusing",
			rawPath, joined.Scheme, joined.Host, base)
	}
	if !hasQuery {
		return u, nil
	}
	if _, err := url.ParseQuery(query); err != nil {
		return "", fmt.Errorf("invalid query string %q: %w", query, err)
	}
	joined.RawQuery = query
	return joined.String(), nil
}
