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
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == 404
}
