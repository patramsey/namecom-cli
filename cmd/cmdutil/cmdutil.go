// Package cmdutil provides helpers for retrieving shared state (output config,
// API client, config file) from a cobra command's context. The values are
// stored there by cmd/root.go's PersistentPreRunE.
package cmdutil

import (
	"errors"

	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

type contextKey int

// Context keys for values stored by cmd/root.go's PersistentPreRunE.
const (
	KeyOutput contextKey = iota
	KeyClient
	KeyConfig
	KeyOverrides
)

// Out retrieves the output.Config from the command context.
func Out(cmd *cobra.Command) *output.Config {
	if v := cmd.Context().Value(KeyOutput); v != nil {
		return v.(*output.Config)
	}
	return output.DefaultConfig()
}

// APIClient retrieves the api.Client from the command context.
func APIClient(cmd *cobra.Command) *api.Client {
	return cmd.Context().Value(KeyClient).(*api.Client)
}

// CfgFile retrieves the loaded config.File from the command context.
func CfgFile(cmd *cobra.Command) *config.File {
	return cmd.Context().Value(KeyConfig).(*config.File)
}

// IsYes reports whether --yes / -y was passed on the root command.
func IsYes(cmd *cobra.Command) bool {
	root := cmd.Root()
	f := root.PersistentFlags().Lookup("yes")
	return f != nil && f.Value.String() == "true"
}

// IsDryRun reports whether --dry-run was passed on the root command.
func IsDryRun(cmd *cobra.Command) bool {
	root := cmd.Root()
	f := root.PersistentFlags().Lookup("dry-run")
	return f != nil && f.Value.String() == "true"
}

// Overrides retrieves the resolved config.Overrides from the command context.
func Overrides(cmd *cobra.Command) config.Overrides {
	if v := cmd.Context().Value(KeyOverrides); v != nil {
		return v.(config.Overrides)
	}
	return config.Overrides{}
}

// IsSandbox reports whether this invocation targets the sandbox API.
//
// Sandbox mode has three sources — the --sandbox flag, NAMECOM_SANDBOX, and a
// profile's `sandbox: true` — and root.go resolves all three into the output
// config. Reading only the flag meant sandbox-by-profile users were silently
// treated as production by callers relying on this. Fall back to the flag for
// contexts where the output config hasn't been built yet.
func IsSandbox(cmd *cobra.Command) bool {
	if out := Out(cmd); out != nil && out.Sandbox {
		return true
	}
	f := cmd.Root().PersistentFlags().Lookup("sandbox")
	return f != nil && f.Value.String() == "true"
}

// IsNotFound reports whether err is a 404 API error.
func IsNotFound(err error) bool {
	// Normalized first: an SDK error that was never converted is still a 404,
	// and checking only for *api.APIError is why the friendly not-found
	// messages in domain get and transfer get never fired.
	var apiErr *api.APIError
	return errors.As(api.NormalizeError(err), &apiErr) && apiErr.StatusCode == 404
}

// NotFound replaces a not-found error's message with a friendlier one while
// keeping the error underneath, so the command still exits 4.
//
// Five commands turned a detected 404 into fmt.Errorf("… not found — run …").
// The message improved and the classification was thrown away: exitCode found
// no *api.APIError in a plain string error and returned 1.
func NotFound(err error, msg string) error {
	return &notFoundError{msg: msg, err: api.NormalizeError(err)}
}

type notFoundError struct {
	msg string
	err error
}

func (e *notFoundError) Error() string { return e.msg }
func (e *notFoundError) Unwrap() error { return e.err }
