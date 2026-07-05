package email

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

func cmdForEmailCreate(t *testing.T, srv *httptest.Server) *cobra.Command {
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
	cmd.Flags().StringVar(&createEmailTo, "to", "", "")
	return cmd
}

func TestEmailCreate_MailboxWithAt(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForEmailCreate(t, srv)
	createEmailTo = "dest@gmail.com"

	// mailbox arg contains @ — should fail before any API call
	err := runCreate(cmd, []string{"example.com", "info@example.com"})
	if err == nil {
		t.Fatal("expected error for mailbox containing '@', got nil")
	}
	if !strings.Contains(err.Error(), "@") {
		t.Errorf("expected '@' mentioned in error, got: %v", err)
	}
}

func TestEmailCreate_MailboxEmpty(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForEmailCreate(t, srv)
	createEmailTo = "dest@gmail.com"

	err := runCreate(cmd, []string{"example.com", ""})
	if err == nil {
		t.Fatal("expected error for empty mailbox, got nil")
	}
}

func TestEmailCreate_MailboxWithSpace(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForEmailCreate(t, srv)
	createEmailTo = "dest@gmail.com"

	err := runCreate(cmd, []string{"example.com", "hello world"})
	if err == nil {
		t.Fatal("expected error for mailbox with space, got nil")
	}
	if !strings.Contains(err.Error(), "space") {
		t.Errorf("expected 'space' in error, got: %v", err)
	}
}

func TestEmailCreate_InvalidToAddress(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForEmailCreate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "notanemail"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	err := runCreate(cmd, []string{"example.com", "info"})
	if err == nil {
		t.Fatal("expected error for invalid --to email, got nil")
	}
	if !strings.Contains(err.Error(), "email") {
		t.Errorf("expected 'email' in error, got: %v", err)
	}
}

func TestEmailCreate_ToNoDomainDot(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForEmailCreate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "user@nodot"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	err := runCreate(cmd, []string{"example.com", "info"})
	if err == nil {
		t.Fatal("expected error for --to with domain missing dot, got nil")
	}
}

func TestEmailCreate_BadDomainArg(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForEmailCreate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "user@example.com"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	err := runCreate(cmd, []string{"nodot", "info"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

func TestEmailCreate_DomainNormalized(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domainName":"example.com","emailBox":"info","emailTo":"dest@gmail.com"}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForEmailCreate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "dest@gmail.com"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	if err := runCreate(cmd, []string{"EXAMPLE.COM", "info"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	if !strings.Contains(receivedPath, "example.com") {
		t.Errorf("expected normalized 'example.com' in request path, got %q", receivedPath)
	}
	if strings.Contains(receivedPath, "EXAMPLE") {
		t.Errorf("domain was not lowercased in request path: %q", receivedPath)
	}
}

// ---- email update -----------------------------------------------------------

func cmdForEmailUpdate(t *testing.T, srv *httptest.Server) *cobra.Command {
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
	cmd.Flags().StringVar(&updateEmailTo, "to", "", "")
	return cmd
}

func TestEmailUpdate_InvalidToAddress(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForEmailUpdate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "notanemail"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	err := runUpdate(cmd, []string{"example.com", "info"})
	if err == nil {
		t.Fatal("expected error for invalid --to email, got nil")
	}
}

func TestEmailUpdate_BadDomainArg(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForEmailUpdate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "user@example.com"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	err := runUpdate(cmd, []string{"nodot", "info"})
	if err == nil {
		t.Fatal("expected error for domain without dot in update, got nil")
	}
}

// ---- email list -------------------------------------------------------------

func cmdForEmailList(t *testing.T, srv *httptest.Server) *cobra.Command {
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
	cmd.Flags().BoolVar(&listAll, "all", false, "")
	t.Cleanup(func() { listAll = false })
	return cmd
}

func TestEmailList_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForEmailList(t, srv)
	if err := runList(cmd, []string{"nodot"}); err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

func TestEmailList_Empty(t *testing.T) {
	var nextPage int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gen.ListEmailForwardingsResponseSchema{NextPage: &nextPage})
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForEmailList(t, srv)
	if err := runList(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runList: %v", err)
	}
}

func TestEmailList_ShowsEntries(t *testing.T) {
	var nextPage int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		entries := []gen.EmailForwarding{
			{EmailBox: "info", DomainName: "example.com", EmailTo: "dest@gmail.com"},
		}
		_ = json.NewEncoder(w).Encode(gen.ListEmailForwardingsResponseSchema{
			EmailForwarding: entries,
			NextPage:        &nextPage,
		})
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

	if err := runList(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if !strings.Contains(stdout.String(), "info") {
		t.Errorf("expected 'info' in output, got: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "dest@gmail.com") {
		t.Errorf("expected 'dest@gmail.com' in output, got: %q", stdout.String())
	}
}

// ---- email get --------------------------------------------------------------

func cmdForEmailGet(t *testing.T, srv *httptest.Server) *cobra.Command {
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

func TestEmailGet_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForEmailGet(t, srv)
	if err := runGet(cmd, []string{"nodot", "info"}); err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

func TestEmailGet_ReturnsEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gen.EmailForwarding{
			EmailBox:   "info",
			DomainName: "example.com",
			EmailTo:    "dest@gmail.com",
		})
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForEmailGet(t, srv)
	if err := runGet(cmd, []string{"example.com", "info"}); err != nil {
		t.Fatalf("runGet: %v", err)
	}
}

// ---- email delete -----------------------------------------------------------

func cmdForEmailDelete(t *testing.T, srv *httptest.Server) *cobra.Command {
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

func TestEmailDelete_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForEmailDelete(t, srv)
	if err := runDelete(cmd, []string{"nodot", "info"}); err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

func TestEmailDelete_DeletesEntry(t *testing.T) {
	var deletePath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deletePath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForEmailDelete(t, srv)
	if err := runDelete(cmd, []string{"example.com", "info"}); err != nil {
		t.Fatalf("runDelete: %v", err)
	}
	if !strings.Contains(deletePath, "example.com") || !strings.Contains(deletePath, "info") {
		t.Errorf("unexpected delete path: %q", deletePath)
	}
}
