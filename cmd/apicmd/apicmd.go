// Package apicmd implements the `namecom api` raw HTTP passthrough command.
package apicmd

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/namedotcom/namecom-cli/cmd/cmdutil"
	"github.com/spf13/cobra"
)

// Cmd is the `namecom api` command.
var Cmd = &cobra.Command{
	Use:   "api <METHOD> <path>",
	Short: "Make a raw API request",
	Long: `Make a raw HTTP request to the name.com API.

Examples:
  namecom api GET /core/v1/domains
  namecom api GET /core/v1/domains/example.com
  echo '{"host":"@","type":"A","answer":"1.2.3.4","ttl":300}' | namecom api POST /core/v1/domains/example.com/records`,
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
	u, err := url.JoinPath(base, rawPath)
	if err != nil {
		return fmt.Errorf("building URL: %w", err)
	}

	var bodyReader io.Reader
	if apiBody == "-" {
		bodyReader = os.Stdin
	} else if apiBody != "" {
		bodyReader = strings.NewReader(apiBody)
	}

	req, err := http.NewRequestWithContext(cmd.Context(), method, u, bodyReader)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, h := range apiHeaders {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid header %q: expected 'Name: Value'", h)
		}
		req.Header.Set(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
	}

	// Use the raw http.Client from the generated client to apply auth/rate-limiting.
	resp, err := client.HTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(out.EWriter, "HTTP %d\n", resp.StatusCode)
		_, _ = os.Stderr.Write(body)
		fmt.Fprintln(os.Stderr)
		return fmt.Errorf("API error: HTTP %d", resp.StatusCode)
	}

	fmt.Fprintf(out.Writer, "%s\n", body)
	return nil
}
