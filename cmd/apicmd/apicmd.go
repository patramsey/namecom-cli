// Package apicmd implements the `namecom api` raw HTTP passthrough command.
package apicmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/spf13/cobra"
)

// Cmd is the `namecom api` command.
var Cmd = &cobra.Command{
	Use:   "api <METHOD> <path>",
	Short: "Make a raw API request",
	Long: `Make a raw HTTP request to the name.com API. Auth, rate limiting, and retries are applied automatically.

With --dry-run, any method other than GET or HEAD is printed — method, path,
and body — instead of sent. GET and HEAD still run.`,
	Example: `  namecom api GET /core/v1/domains
  namecom api GET /core/v1/domains/example.com
  namecom api POST /core/v1/domains/example.com/records --data '{"host":"@","type":"A","answer":"1.2.3.4","ttl":300}'
  echo '{"host":"www","type":"CNAME","answer":"example.com.","ttl":300}' | namecom api POST /core/v1/domains/example.com/records`,
	Args: cmdutil.ExactArgs(2),
	RunE: runAPI,
}

var (
	apiBody    string
	apiHeaders []string
)

func init() {
	Cmd.Flags().StringVar(&apiBody, "data", "", "request body (JSON); use '-' to read from stdin")
	Cmd.Flags().StringArrayVar(&apiHeaders, "header", nil, "additional headers: 'Name: Value'")
}

func runAPI(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	method := strings.ToUpper(args[0])
	rawPath := args[1]

	base := client.BaseURL()
	u, err := buildAPIURL(base, rawPath)
	if err != nil {
		return err
	}

	// Read the body once, up front: stdin cannot be read twice, and --dry-run
	// must preview the bytes the request would carry. The retry transport
	// buffers the body for replay anyway, so streaming it saved nothing.
	var body []byte
	switch apiBody {
	case "":
	case "-":
		if body, err = io.ReadAll(os.Stdin); err != nil {
			return fmt.Errorf("reading request body from stdin: %w", err)
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

	send := func(ctx context.Context, body []byte) error {
		var bodyReader io.Reader
		if apiBody != "" {
			bodyReader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
		if err != nil {
			return fmt.Errorf("building request: %w", err)
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
			return fmt.Errorf("preparing request: %w", err)
		}
		resp, err := client.HTTPClient().Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("reading response: %w", err)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			fmt.Fprintf(out.EWriter, "HTTP %d\n", resp.StatusCode)
			_, _ = os.Stderr.Write(respBody)
			fmt.Fprintln(os.Stderr)
			// Return the normalized error type so root.go's exit-code mapping and
			// UserHint apply here too. A plain fmt.Errorf collapsed every failure to
			// exit 1, hiding the documented auth/rate-limit codes from scripts.
			return api.ErrorFromResponse(resp.StatusCode, respBody)
		}

		fmt.Fprintf(out.Writer, "%s\n", respBody)
		return nil
	}

	// Reads still run under --dry-run, as the flag's help promises. Every
	// other method is previewed: a raw passthrough cannot tell whether a POST
	// changes anything (checkAvailability does not; POST /domains buys a
	// domain), so it treats them all as writes.
	if method == http.MethodGet || method == http.MethodHead {
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
	_, err = cmdutil.RunWrite(cmd, cmdutil.Write[any]{
		Method: method, Path: parsed.RequestURI(), Body: payload,
	}, func(ctx context.Context, b any) error {
		rb, _ := b.(rawBody) // nil for NoBody
		return send(ctx, rb)
	})
	return err
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
