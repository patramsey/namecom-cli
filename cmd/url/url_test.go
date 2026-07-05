package url

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

// ensure context import used across all tests
var _ = context.Background

func neverCalledServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("API should not be called for pre-flight validation failure: %s %s", r.Method, r.URL)
		http.Error(w, "unexpected call", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func cmdForURLCreate(t *testing.T, srv *httptest.Server) *cobra.Command {
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
	cmd.Flags().StringVar(&createForwardsTo, "to", "", "")
	cmd.Flags().StringVar(&createType, "type", "redirect", "")
	cmd.Flags().StringVar(&createHost, "host", "@", "")
	cmd.Flags().StringVar(&createTitle, "title", "", "")
	cmd.Flags().StringVar(&createMeta, "meta", "", "")
	return cmd
}

func TestURLCreate_MissingDestURL(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForURLCreate(t, srv)
	createForwardsTo, createType, createHost = "", "redirect", "@"

	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error when --to is empty, got nil")
	}
}

func TestURLCreate_DestURLNoScheme(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForURLCreate(t, srv)
	// Mark --to as Changed so the validation path runs (non-interactive).
	if err := cmd.ParseFlags([]string{"--to", "example.com"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	createType = "redirect"

	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for URL without http:// scheme, got nil")
	}
	if !strings.Contains(err.Error(), "http") {
		t.Errorf("expected 'http' in error, got: %v", err)
	}
}

func TestURLCreate_InvalidType(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForURLCreate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "https://example.com", "--type", "permanent"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for invalid forwarding type, got nil")
	}
	if !strings.Contains(err.Error(), "redirect") && !strings.Contains(err.Error(), "302") {
		t.Errorf("expected valid types listed in error, got: %v", err)
	}
}

func TestURLCreate_ValidTypes(t *testing.T) {
	for _, fwdType := range []string{"redirect", "302", "masked"} {
		t.Run(fwdType, func(t *testing.T) {
			var called bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"host":"@","forwardsTo":"https://example.com","type":"` + fwdType + `"}`))
			}))
			t.Cleanup(srv.Close)

			cmd := cmdForURLCreate(t, srv)
			if err := cmd.ParseFlags([]string{"--to", "https://example.com", "--type", fwdType}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}

			if err := runCreate(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("runCreate with type %q: %v", fwdType, err)
			}
			if !called {
				t.Errorf("expected API to be called for valid type %q", fwdType)
			}
		})
	}
}

func TestURLCreate_BadDomainArg(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForURLCreate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "https://example.com"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	err := runCreate(cmd, []string{"nodot"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

// ---- url update -------------------------------------------------------------

func cmdForURLUpdate(t *testing.T, srv *httptest.Server) *cobra.Command {
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
	cmd.Flags().StringVar(&updateForwardsTo, "to", "", "")
	cmd.Flags().StringVar(&updateType, "type", "redirect", "")
	cmd.Flags().StringVar(&updateTitle, "title", "", "")
	cmd.Flags().StringVar(&updateMeta, "meta", "", "")
	t.Cleanup(func() { updateForwardsTo = ""; updateType = "redirect"; updateTitle = ""; updateMeta = "" })
	return cmd
}

func TestURLUpdate_BadID(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForURLUpdate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "https://dest.com"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := runUpdate(cmd, []string{"example.com", "notanumber"})
	if err == nil {
		t.Fatal("expected error for non-integer ID, got nil")
	}
}

func TestURLUpdate_BadDestURL(t *testing.T) {
	getResponse := `{"id":1,"host":"@","forwardsTo":"https://old.com","type":"redirect"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(getResponse))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForURLUpdate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "no-scheme.com"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := runUpdate(cmd, []string{"example.com", "1"})
	if err == nil {
		t.Fatal("expected error for URL without http:// scheme, got nil")
	}
	if !strings.Contains(err.Error(), "http") {
		t.Errorf("expected 'http' in error, got: %v", err)
	}
}

func TestURLUpdate_InvalidType(t *testing.T) {
	getResponse := `{"id":1,"host":"@","forwardsTo":"https://old.com","type":"redirect"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			t.Error("PUT should not be called when type validation fails")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(getResponse))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForURLUpdate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "https://dest.com", "--type", "permanent"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := runUpdate(cmd, []string{"example.com", "1"})
	if err == nil {
		t.Fatal("expected error for invalid forwarding type, got nil")
	}
}

// ---- url delete -------------------------------------------------------------

func cmdForURLDelete(t *testing.T, srv *httptest.Server) *cobra.Command {
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
	var yes bool
	cmd.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
	if err := cmd.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	return cmd
}

func TestURLDelete_BadID(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForURLDelete(t, srv)
	err := runDelete(cmd, []string{"example.com", "notanumber"})
	if err == nil {
		t.Fatal("expected error for non-integer ID, got nil")
	}
}

func TestURLDelete_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForURLDelete(t, srv)
	err := runDelete(cmd, []string{"nodot", "1"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}
