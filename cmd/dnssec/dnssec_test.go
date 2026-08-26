package dnssec

import (
	"bytes"
	"context"
	"encoding/json"
	coreapigo "github.com/namedotcom/core-api-go"
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

func cmdForCreate(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	cmd := baseCmd(t, srv)
	cmd.Flags().Int32Var(&createAlgorithm, "algorithm", 0, "")
	cmd.Flags().StringVar(&createDigest, "digest", "", "")
	cmd.Flags().Int32Var(&createDigestType, "digest-type", 0, "")
	cmd.Flags().Int32Var(&createKeyTag, "key-tag", 0, "")
	t.Cleanup(func() { createAlgorithm = 0; createDigest = ""; createDigestType = 0; createKeyTag = 0 })
	return cmd
}

func cmdWithYes(t *testing.T, srv *httptest.Server) *cobra.Command {
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

func TestDNSSECList_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := baseCmd(t, srv)
	err := runList(cmd, []string{"nodot"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
	if !strings.Contains(err.Error(), "dot") {
		t.Errorf("expected 'dot' in error, got: %v", err)
	}
}

func TestDNSSECList_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(coreapigo.ListDnsseCsResponse{})
	}))
	t.Cleanup(srv.Close)

	cmd := baseCmd(t, srv)
	if err := runList(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	// An empty result must still guide the user — the point of the empty state
	// is that someone with no records is told what to do next, not shown a
	// blank screen. Asserting err == nil alone cannot see that.
	buf, ok := cmdutil.Out(cmd).Writer.(*bytes.Buffer)
	if !ok {
		t.Fatal("output writer is not a *bytes.Buffer")
	}
	got := buf.String()
	if !strings.Contains(got, "No DNSSEC keys found") {
		t.Errorf("empty-state output should contain %q, got:\n%s", "No DNSSEC keys found", got)
	}
	if !strings.Contains(got, "dnssec create") {
		t.Errorf("empty-state output should contain %q, got:\n%s", "dnssec create", got)
	}
}

// ---- create -----------------------------------------------------------------

func TestDNSSECCreate_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForCreate(t, srv)
	createAlgorithm, createDigest, createDigestType, createKeyTag = 8, "abc123", 2, 12345

	err := runCreate(cmd, []string{"nodot"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
	if !strings.Contains(err.Error(), "dot") {
		t.Errorf("expected 'dot' in error, got: %v", err)
	}
}

func TestDNSSECCreate_DomainNormalized(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(coreapigo.Dnssec{Digest: "abc123"})
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForCreate(t, srv)
	createAlgorithm, createDigest, createDigestType, createKeyTag = 8, "abc123", 2, 12345

	if err := runCreate(cmd, []string{"EXAMPLE.COM"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	if strings.Contains(receivedPath, "EXAMPLE") {
		t.Errorf("domain was not normalized in request path: %q", receivedPath)
	}
	if !strings.Contains(receivedPath, "example.com") {
		t.Errorf("expected normalized 'example.com' in path, got: %q", receivedPath)
	}
}

// ---- get --------------------------------------------------------------------

func TestDNSSECGet_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := baseCmd(t, srv)
	err := runGet(cmd, []string{"nodot", "abc123"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
	if !strings.Contains(err.Error(), "dot") {
		t.Errorf("expected 'dot' in error, got: %v", err)
	}
}

func TestDNSSECGet_ReturnsKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(coreapigo.Dnssec{KeyTag: 1234, Digest: "abc123"})
	}))
	t.Cleanup(srv.Close)

	cmd := baseCmd(t, srv)
	if err := runGet(cmd, []string{"example.com", "abc123"}); err != nil {
		t.Fatalf("runGet: %v", err)
	}

	// See the note in cmd/url: err == nil says nothing about what was rendered.
	buf, ok := cmdutil.Out(cmd).Writer.(*bytes.Buffer)
	if !ok {
		t.Fatal("output writer is not a *bytes.Buffer")
	}
	got := buf.String()
	for _, want := range []string{"1234", "abc123"} {
		if !strings.Contains(got, want) {
			t.Errorf("get output is missing %q:\n%s", want, got)
		}
	}
}

// ---- delete -----------------------------------------------------------------

func TestDNSSECDelete_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdWithYes(t, srv)
	err := runDelete(cmd, []string{"nodot", "abc123"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
	if !strings.Contains(err.Error(), "dot") {
		t.Errorf("expected 'dot' in error, got: %v", err)
	}
}

func TestDNSSECDelete_DomainNormalized(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdWithYes(t, srv)
	if err := runDelete(cmd, []string{"EXAMPLE.COM", "abc123"}); err != nil {
		t.Fatalf("runDelete: %v", err)
	}
	if strings.Contains(receivedPath, "EXAMPLE") {
		t.Errorf("domain was not normalized in DELETE path: %q", receivedPath)
	}
	if !strings.Contains(receivedPath, "example.com") {
		t.Errorf("expected normalized 'example.com' in DELETE path, got: %q", receivedPath)
	}
}

// TestDNSSECList_JSONUsesDataEnvelope pins dnssec list to the same output shape
// as every other list command (email, url, vanity, transfer, order), which all
// emit {"data":[…]} via out.JSONList. dnssec emitted a bare array, so a script
// written as `namecom email list -o json | jq '.data[]'` silently produced
// nothing when pointed at dnssec.
func TestDNSSECList_JSONUsesDataEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(coreapigo.ListDnsseCsResponse{
			Dnssec: []*coreapigo.Dnssec{{KeyTag: 1234, Algorithm: 8, DigestType: 2, Digest: "ABCD"}},
		})
	}))
	t.Cleanup(srv.Close)

	var buf bytes.Buffer
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	out := &output.Config{
		Format: output.FormatJSON, Color: output.ColorNever,
		Writer: &buf, EWriter: &bytes.Buffer{},
	}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)

	if err := runList(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runList: %v", err)
	}

	var env struct {
		Data []coreapigo.Dnssec `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("output is not a data envelope: %v\noutput: %s", err, buf.String())
	}
	if len(env.Data) != 1 {
		t.Fatalf("expected 1 key under .data, got %d: %s", len(env.Data), buf.String())
	}
	if env.Data[0].Digest != "ABCD" {
		t.Errorf("expected digest ABCD, got %q", env.Data[0].Digest)
	}
}

// TestDNSSECCreate_SendsEachFieldToItsOwnKey covers a request body that had no
// guard at all: nothing decoded what `dnssec create` sends, so swapping
// Algorithm with KeyTag — or blanking the digest — left all nine tests green.
//
// The values are deliberately distinct so a transposition cannot pass. DNSSEC
// key material is submitted to the registry; a silently mis-mapped field
// produces a DS record that does not validate, which breaks resolution for the
// zone rather than merely erroring.
func TestDNSSECCreate_SendsEachFieldToItsOwnKey(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decoding create body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"digest":"ABC123"}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForCreate(t, srv)
	if err := cmd.ParseFlags([]string{
		"--algorithm", "8",
		"--digest-type", "2",
		"--key-tag", "54321",
		"--digest", "ABC123DEF456",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	if err := runCreate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	if got == nil {
		t.Fatal("create request was never sent")
	}

	for key, want := range map[string]any{
		"algorithm":  float64(8),
		"digestType": float64(2),
		"keyTag":     float64(54321),
		"digest":     "ABC123DEF456",
	} {
		if got[key] != want {
			t.Errorf("body[%q] = %#v, want %#v (full body: %#v)", key, got[key], want, got)
		}
	}
}
