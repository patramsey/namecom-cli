package domain

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

func neverCalledServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("API should not be called for pre-flight validation failure: %s %s", r.Method, r.URL)
		http.Error(w, "unexpected call", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func baseCmd(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	out := &output.Config{
		Format:  output.FormatTable,
		Color:   output.ColorNever,
		Writer:  &bytes.Buffer{},
		EWriter: &bytes.Buffer{},
	}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	return cmd
}

// ---- set-ns -----------------------------------------------------------------

func cmdForSetNS(t *testing.T, srv *httptest.Server) *cobra.Command {
	cmd := baseCmd(t, srv)
	cmd.Flags().StringVar(&setNSList, "ns", "", "")
	return cmd
}

func TestSetNS_InvalidNameserver(t *testing.T) {
	tests := []struct {
		desc, ns    string
		errContains string
	}{
		{"no dot", "ns1nodot", "fully-qualified"},
		{"empty entry", "ns1.example.com,", "empty"},
		{"leading hyphen", "-ns1.example.com", "hyphen"},
		{"leading dot", ".ns1.example.com", "dot"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			srv := neverCalledServer(t)
			cmd := cmdForSetNS(t, srv)
			if err := cmd.ParseFlags([]string{"--ns", tt.ns}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			err := runSetNS(cmd, []string{"example.com"})
			if err == nil {
				t.Fatalf("expected error for NS %q, got nil", tt.ns)
			}
			if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("expected %q in error, got: %v", tt.errContains, err)
			}
		})
	}
}

func TestSetNS_BadDomainArg(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForSetNS(t, srv)
	if err := cmd.ParseFlags([]string{"--ns", "ns1.example.com"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := runSetNS(cmd, []string{"nodot"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

// ---- register years ---------------------------------------------------------

func cmdForRegister(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	cmd := baseCmd(t, srv)
	cmd.Flags().IntVar(&registerYears, "years", 1, "")
	cmd.Flags().BoolVar(&registerPrivacy, "privacy", false, "")
	cmd.Flags().BoolVar(&registerAutorenew, "autorenew", false, "")
	cmd.Flags().StringVar(&registerContactsFile, "contacts-file", "", "")
	cmd.Flags().Float64Var(&registerPrice, "price", 0, "")
	cmd.Flags().StringVar(&registerIdemKey, "idempotency-key", "", "")
	var yes bool
	cmd.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
	return cmd
}

func TestRegister_YearsOutOfRange(t *testing.T) {
	for _, years := range []string{"0", "11", "100"} {
		t.Run("years="+years, func(t *testing.T) {
			srv := neverCalledServer(t)
			cmd := cmdForRegister(t, srv)
			// ParseFlags marks --years as Changed, triggering ValidYears.
			if err := cmd.ParseFlags([]string{"--years", years}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			err := runRegister(cmd, []string{"example.com"})
			if err == nil {
				t.Fatalf("expected error for --years %s, got nil", years)
			}
			if !strings.Contains(err.Error(), "years") {
				t.Errorf("expected 'years' in error, got: %v", err)
			}
		})
	}
}
