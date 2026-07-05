package vanity

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/api/gen"
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

func cmdForCreate(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	cmd := baseCmd(t, srv)
	cmd.Flags().StringVar(&createHostname, "hostname", "", "")
	cmd.Flags().StringVar(&createIPs, "ips", "", "")
	t.Cleanup(func() { createHostname = ""; createIPs = "" })
	return cmd
}

func cmdForUpdate(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	cmd := baseCmd(t, srv)
	cmd.Flags().StringVar(&updateIPs, "ips", "", "")
	t.Cleanup(func() { updateIPs = "" })
	return cmd
}

func cmdForDelete(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	cmd := baseCmd(t, srv)
	var yes bool
	cmd.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
	if err := cmd.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	return cmd
}

// ---- list -------------------------------------------------------------------

func TestVanityList_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := baseCmd(t, srv)
	cmd.Flags().BoolVar(&listAll, "all", false, "")
	t.Cleanup(func() { listAll = false })
	err := runList(cmd, []string{"nodot"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

func TestVanityList_Empty(t *testing.T) {
	var nextPage int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gen.ListVanityNameserversResponseSchema{NextPage: &nextPage})
	}))
	t.Cleanup(srv.Close)

	cmd := baseCmd(t, srv)
	cmd.Flags().BoolVar(&listAll, "all", false, "")
	t.Cleanup(func() { listAll = false })
	if err := runList(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runList: %v", err)
	}
}

// ---- create -----------------------------------------------------------------

func TestVanityCreate_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForCreate(t, srv)
	createHostname, createIPs = "ns1.example.com", "1.2.3.4"
	err := runCreate(cmd, []string{"nodot"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

func TestVanityCreate_DomainNormalized(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gen.VanityNameserverResponseSchema{Hostname: "ns1.example.com"})
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForCreate(t, srv)
	createHostname, createIPs = "ns1.example.com", "1.2.3.4"
	if err := runCreate(cmd, []string{"EXAMPLE.COM"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	if strings.Contains(receivedPath, "EXAMPLE") {
		t.Errorf("domain not normalized in create path: %q", receivedPath)
	}
	if !strings.Contains(receivedPath, "example.com") {
		t.Errorf("expected 'example.com' in create path, got: %q", receivedPath)
	}
}

// ---- update -----------------------------------------------------------------

func TestVanityUpdate_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForUpdate(t, srv)
	updateIPs = "1.2.3.4"
	err := runUpdate(cmd, []string{"nodot", "ns1.example.com"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

// ---- delete -----------------------------------------------------------------

func TestVanityDelete_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForDelete(t, srv)
	err := runDelete(cmd, []string{"nodot", "ns1.example.com"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

func TestVanityDelete_DomainNormalized(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForDelete(t, srv)
	if err := runDelete(cmd, []string{"EXAMPLE.COM", "ns1.example.com"}); err != nil {
		t.Fatalf("runDelete: %v", err)
	}
	if strings.Contains(receivedPath, "EXAMPLE") {
		t.Errorf("domain not normalized in delete path: %q", receivedPath)
	}
	if !strings.Contains(receivedPath, "example.com") {
		t.Errorf("expected 'example.com' in delete path, got: %q", receivedPath)
	}
}
