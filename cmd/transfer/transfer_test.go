package transfer

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

var _ = context.Background // ensure context import is used

func neverCalledServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("API should not be called for pre-flight validation failure: %s %s", r.Method, r.URL)
		http.Error(w, "unexpected call", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func cmdForTransferCreate(t *testing.T, srv *httptest.Server) *cobra.Command {
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
	// Register --yes on persistent flags so cmdutil.IsYes returns true (skips confirm).
	var yes bool
	cmd.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
	cmd.Flags().StringVar(&createAuthCode, "auth-code", "", "")
	cmd.Flags().BoolVar(&createPrivacy, "privacy", false, "")
	cmd.Flags().Float64Var(&createPrice, "price", 0, "")
	return cmd
}

func TestTransferCreate_AuthCodeEmpty(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForTransferCreate(t, srv)
	createAuthCode, createPrivacy, createPrice = "", false, 0

	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for empty auth code, got nil")
	}
	if !strings.Contains(err.Error(), "auth-code") {
		t.Errorf("expected 'auth-code' in error, got: %v", err)
	}
}

func TestTransferCreate_AuthCodeTooShort(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForTransferCreate(t, srv)
	if err := cmd.ParseFlags([]string{"--auth-code", "abc"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for short auth code (< 6 chars), got nil")
	}
	if !strings.Contains(err.Error(), "too short") {
		t.Errorf("expected 'too short' in error, got: %v", err)
	}
}

func TestTransferCreate_BadDomainArg(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForTransferCreate(t, srv)
	if err := cmd.ParseFlags([]string{"--auth-code", "validcode123"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	err := runCreate(cmd, []string{"nodot"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

// ---- internal-in ------------------------------------------------------------

func cmdForInternalIn(t *testing.T, srv *httptest.Server) *cobra.Command {
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
	cmd.Flags().StringVar(&internalAuthCode, "auth-code", "", "")
	t.Cleanup(func() { internalAuthCode = "" })
	return cmd
}

func TestTransferInternalIn_EmptyAuthCode(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForInternalIn(t, srv)
	if err := cmd.ParseFlags([]string{"--auth-code", ""}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := runInternalIn(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for empty auth code in non-interactive mode, got nil")
	}
}

func TestTransferInternalIn_AuthCodeTooShort(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForInternalIn(t, srv)
	if err := cmd.ParseFlags([]string{"--auth-code", "abc"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := runInternalIn(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for short auth code, got nil")
	}
	if !strings.Contains(err.Error(), "too short") {
		t.Errorf("expected 'too short' in error, got: %v", err)
	}
}

func TestTransferInternalIn_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForInternalIn(t, srv)
	if err := cmd.ParseFlags([]string{"--auth-code", "validcode123"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := runInternalIn(cmd, []string{"nodot"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

// ---- transfer list ----------------------------------------------------------

func cmdForTransferList(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &bytes.Buffer{}, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	cmd.Flags().BoolVar(&listAll, "all", false, "")
	t.Cleanup(func() { listAll = false })
	return cmd
}

func TestTransferList_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"transfers":[],"nextPage":0}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForTransferList(t, srv)
	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
}

func TestTransferList_ShowsEntries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"transfers":[{"domainName":"example.com","status":"pending"}],"nextPage":0}`))
	}))
	t.Cleanup(srv.Close)

	var stdout bytes.Buffer
	client, _ := api.New(api.Options{BaseURL: srv.URL})
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &stdout, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	cmd.Flags().BoolVar(&listAll, "all", false, "")
	t.Cleanup(func() { listAll = false })

	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if !strings.Contains(stdout.String(), "example.com") {
		t.Errorf("expected 'example.com' in output, got: %q", stdout.String())
	}
}

// ---- transfer get -----------------------------------------------------------

func cmdForTransferGet(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &bytes.Buffer{}, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	return cmd
}

func TestTransferGet_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForTransferGet(t, srv)
	if err := runGet(cmd, []string{"nodot"}); err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

func TestTransferGet_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domainName":"example.com","status":"pending"}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForTransferGet(t, srv)
	if err := runGet(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runGet: %v", err)
	}
}

func TestTransferInternalIn_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domainName":"example.com","status":"pending"}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForInternalIn(t, srv)
	if err := cmd.ParseFlags([]string{"--auth-code", "validcode123"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := runInternalIn(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runInternalIn: %v", err)
	}
}

func TestTransferCreate_DomainNormalized(t *testing.T) {
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		receivedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domainName":"example.com","status":"pending","order":1,"totalPaid":9.99}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForTransferCreate(t, srv)
	if err := cmd.ParseFlags([]string{"--auth-code", "validcode123", "--yes"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	if err := runCreate(cmd, []string{"EXAMPLE.COM"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	body := string(receivedBody)
	if !strings.Contains(body, `"example.com"`) {
		t.Errorf("expected normalized 'example.com' in request body, got %q", body)
	}
	if strings.Contains(body, "EXAMPLE") {
		t.Errorf("domain was not lowercased in request body: %q", body)
	}
}
