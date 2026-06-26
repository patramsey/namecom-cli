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
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
