package domain

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// ---- shared scaffolding -----------------------------------------------------

// jsonServer serves a fixed body with a fixed status and records the request
// path, so tests can assert both what was rendered and what was asked for.
func jsonServer(t *testing.T, status int, body string) (*httptest.Server, func() string) {
	t.Helper()
	var mu sync.Mutex
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		path = r.URL.Path
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, func() string {
		mu.Lock()
		defer mu.Unlock()
		return path
	}
}

func cmdWithOutput(t *testing.T, srv *httptest.Server, out *output.Config) *cobra.Command {
	t.Helper()
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	return cmd
}

func tableOut(w *bytes.Buffer) *output.Config {
	return &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: w, EWriter: &bytes.Buffer{}}
}

// ---- joinYears --------------------------------------------------------------

func TestJoinYears(t *testing.T) {
	tests := []struct {
		name  string
		years []int
		want  string
	}{
		{"nil renders the em-dash placeholder", nil, "—"},
		{"empty renders the em-dash placeholder", []int{}, "—"},
		{"single year has no separator", []int{1}, "1"},
		{"multiple years are comma-separated", []int{1, 2, 5, 10}, "1, 2, 5, 10"},
		{"order is preserved, not sorted", []int{10, 1, 3}, "10, 1, 3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := joinYears(tt.years); got != tt.want {
				t.Errorf("joinYears(%v) = %q, want %q", tt.years, got, tt.want)
			}
		})
	}
}

// ---- domain requirements ----------------------------------------------------

const requirementsBody = `{
  "tldInfo": {
    "allowedRegistrationYears": [1, 2, 5],
    "supportsDnssec": true,
    "supportsPrivacy": false,
    "supportsTransferLock": true,
    "supportsPremium": false
  },
  "requirements": {"fields": {"registrantType": {}, "idNumber": {}, "auEligibility": {}}},
  "contacts": {}
}`

// The TLD goes into the request path, and the API wants it bare and ASCII —
// no leading dot, lowercase. Sending ".FR" would 404 against a live API.
func TestDomainRequirements_CanonicalizesTLDIntoPath(t *testing.T) {
	srv, reqPath := jsonServer(t, http.StatusOK, requirementsBody)
	cmd := cmdWithOutput(t, srv, tableOut(&bytes.Buffer{}))

	if err := runRequirements(cmd, []string{"  FR  "}); err != nil {
		t.Fatalf("runRequirements: %v", err)
	}
	if got, want := reqPath(), "/core/v1/domaininfo/requirements/fr"; got != want {
		t.Errorf("request path = %q, want %q", got, want)
	}
}

func TestDomainRequirements_EmptyTLDRejectedWithoutCallingAPI(t *testing.T) {
	cmd := cmdWithOutput(t, neverCalledServer(t), tableOut(&bytes.Buffer{}))
	err := runRequirements(cmd, []string{"   "})
	if err == nil {
		t.Fatal("expected an error for a blank TLD, got nil")
	}
	if !strings.Contains(err.Error(), "tld is required") {
		t.Errorf("error should say the TLD is required, got: %v", err)
	}
}

// The table is the default view, and it is the only place the capability flags
// are rendered. A flag rendered with the wrong polarity tells someone a TLD
// supports privacy when it does not.
func TestDomainRequirements_TableRendersCapabilities(t *testing.T) {
	srv, _ := jsonServer(t, http.StatusOK, requirementsBody)
	var stdout bytes.Buffer
	cmd := cmdWithOutput(t, srv, tableOut(&stdout))

	if err := runRequirements(cmd, []string{"fr"}); err != nil {
		t.Fatalf("runRequirements: %v", err)
	}
	got := stdout.String()

	for _, want := range []string{
		"1, 2, 5", // allowedRegistrationYears, via joinYears
		"DNSSEC",
		"WHOIS privacy",
		"Transfer lock",
		"Premium domains",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("table output missing %q, got:\n%s", want, got)
		}
	}
	// supportsDnssec true / supportsPrivacy false — both badges must appear, so
	// a stuck-on-yes or stuck-on-no rendering fails here.
	if !strings.Contains(got, "yes") {
		t.Errorf("expected a 'yes' badge for supportsDnssec, got:\n%s", got)
	}
	if !strings.Contains(got, "no") {
		t.Errorf("expected a 'no' badge for supportsPrivacy, got:\n%s", got)
	}
	// The table deliberately does not flatten the nested requirements; it points
	// at the structured view instead. Losing that hint strands the user.
	if !strings.Contains(got, "-o json") {
		t.Errorf("table should point at the JSON view, got:\n%s", got)
	}
	if !strings.Contains(got, "--tld-requirement") {
		t.Errorf("table should explain how to pass requirements at registration, got:\n%s", got)
	}
}

// Quiet mode exists so `--tld-requirement` arguments can be built from it.
// Echoing the TLD back would be useless, and unsorted output would make the
// result unstable across runs because Go map iteration is randomized.
func TestDomainRequirements_QuietListsFieldNamesSorted(t *testing.T) {
	srv, _ := jsonServer(t, http.StatusOK, requirementsBody)
	var stdout bytes.Buffer
	out := tableOut(&stdout)
	out.QuietMode = true
	cmd := cmdWithOutput(t, srv, out)

	if err := runRequirements(cmd, []string{"au"}); err != nil {
		t.Fatalf("runRequirements: %v", err)
	}
	got := strings.Fields(strings.TrimSpace(stdout.String()))
	want := []string{"auEligibility", "idNumber", "registrantType"}
	if len(got) != len(want) {
		t.Fatalf("quiet output = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("quiet output = %v, want %v (sorted)", got, want)
			break
		}
	}
	if strings.Contains(stdout.String(), "au\n") {
		t.Error("quiet output should list field names, not echo the TLD back")
	}
}

func TestDomainRequirements_QuietWithNoFieldsPrintsNothing(t *testing.T) {
	srv, _ := jsonServer(t, http.StatusOK,
		`{"tldInfo":{"allowedRegistrationYears":[1]},"requirements":{},"contacts":{}}`)
	var stdout bytes.Buffer
	out := tableOut(&stdout)
	out.QuietMode = true
	cmd := cmdWithOutput(t, srv, out)

	if err := runRequirements(cmd, []string{"com"}); err != nil {
		t.Fatalf("runRequirements: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "" {
		t.Errorf("quiet output should be empty when the TLD requires no fields, got: %q", stdout.String())
	}
}

func TestDomainRequirements_StructuredOutput(t *testing.T) {
	for _, format := range []output.Format{output.FormatJSON, output.FormatYAML} {
		t.Run(string(format), func(t *testing.T) {
			srv, _ := jsonServer(t, http.StatusOK, requirementsBody)
			var stdout bytes.Buffer
			out := tableOut(&stdout)
			out.Format = format
			cmd := cmdWithOutput(t, srv, out)

			if err := runRequirements(cmd, []string{"fr"}); err != nil {
				t.Fatalf("runRequirements: %v", err)
			}
			got := stdout.String()
			// The nested requirements are the reason to use this view at all.
			if !strings.Contains(got, "registrantType") {
				t.Errorf("%s output should carry the nested field requirements, got:\n%s", format, got)
			}
			if strings.Contains(got, "Run 'namecom") {
				t.Errorf("%s output must not contain the human hint, got:\n%s", format, got)
			}
		})
	}
}

// ---- domain claims ----------------------------------------------------------

func TestDomainClaims_BadDomain(t *testing.T) {
	cmd := cmdWithOutput(t, neverCalledServer(t), tableOut(&bytes.Buffer{}))
	if err := runClaims(cmd, []string{"nodot"}); err == nil {
		t.Fatal("expected an error for a domain without a dot, got nil")
	}
}

func TestDomainClaims_HitsClaimsEndpoint(t *testing.T) {
	srv, reqPath := jsonServer(t, http.StatusOK, `{"domain":"example.com","claims":[]}`)
	cmd := cmdWithOutput(t, srv, tableOut(&bytes.Buffer{}))

	if err := runClaims(cmd, []string{"EXAMPLE.COM"}); err != nil {
		t.Fatalf("runClaims: %v", err)
	}
	if got, want := reqPath(), "/core/v1/domaininfo/claims/example.com"; got != want {
		t.Errorf("request path = %q, want %q", got, want)
	}
}

// A domain with no claim and a domain with a claim must be visibly different.
// Reporting "no claims" for a claimed name would send someone into a
// registration that cannot complete without acknowledgement.
func TestDomainClaims_ClaimedVsUnclaimed(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantText   string
		wantAbsent string
	}{
		{
			name:       "no claim reports success and does not demand acknowledgement",
			body:       `{"domain":"example.com","claims":[],"claimsProcessActive":true}`,
			wantText:   "No trademark claims found for example.com",
			wantAbsent: "--acknowledge-claim",
		},
		{
			name:       "empty claim id counts as no claim",
			body:       `{"domain":"example.com","claims":[],"claimId":"","claimsProcessActive":true}`,
			wantText:   "No trademark claims found",
			wantAbsent: "--acknowledge-claim",
		},
		{
			name: "a claim id demands acknowledgement at registration",
			body: `{"domain":"tiktok.page","claims":[],"claimId":"ABC-123",
			        "claimsNotice":"You are receiving this notice...","claimsProcessActive":true}`,
			wantText:   "--acknowledge-claim",
			wantAbsent: "No trademark claims found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := jsonServer(t, http.StatusOK, tt.body)
			var stdout bytes.Buffer
			cmd := cmdWithOutput(t, srv, tableOut(&stdout))
			if err := runClaims(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("runClaims: %v", err)
			}
			got := stdout.String()
			if !strings.Contains(got, tt.wantText) {
				t.Errorf("output should contain %q, got:\n%s", tt.wantText, got)
			}
			if strings.Contains(got, tt.wantAbsent) {
				t.Errorf("output should NOT contain %q, got:\n%s", tt.wantAbsent, got)
			}
		})
	}
}

// "No claims found" means something different when the TLD runs no claims
// process at all — the check was vacuous rather than reassuring.
func TestDomainClaims_InactiveClaimsProcessIsCalledOut(t *testing.T) {
	srv, _ := jsonServer(t, http.StatusOK,
		`{"domain":"example.com","claims":[],"claimsProcessActive":false}`)
	var stdout bytes.Buffer
	cmd := cmdWithOutput(t, srv, tableOut(&stdout))

	if err := runClaims(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runClaims: %v", err)
	}
	if !strings.Contains(stdout.String(), "not currently running a claims process") {
		t.Errorf("an inactive claims process should be stated, got:\n%s", stdout.String())
	}
}

// Quiet mode is specified to print the claim ID and nothing at all otherwise,
// so `-q` output is directly usable in a script.
func TestDomainClaims_QuietPrintsOnlyTheClaimID(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"claimed prints the id", `{"domain":"d.page","claims":[],"claimId":"ABC-123"}`, "ABC-123"},
		{"unclaimed prints nothing", `{"domain":"d.com","claims":[]}`, ""},
		{"empty id prints nothing", `{"domain":"d.com","claims":[],"claimId":""}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := jsonServer(t, http.StatusOK, tt.body)
			var stdout bytes.Buffer
			out := tableOut(&stdout)
			out.QuietMode = true
			cmd := cmdWithOutput(t, srv, out)

			if err := runClaims(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("runClaims: %v", err)
			}
			if got := strings.TrimSpace(stdout.String()); got != tt.want {
				t.Errorf("quiet output = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDomainClaims_StructuredOutput(t *testing.T) {
	for _, format := range []output.Format{output.FormatJSON, output.FormatYAML} {
		t.Run(string(format), func(t *testing.T) {
			srv, _ := jsonServer(t, http.StatusOK,
				`{"domain":"tiktok.page","claims":[],"claimId":"ABC-123"}`)
			var stdout bytes.Buffer
			out := tableOut(&stdout)
			out.Format = format
			cmd := cmdWithOutput(t, srv, out)

			if err := runClaims(cmd, []string{"tiktok.page"}); err != nil {
				t.Fatalf("runClaims: %v", err)
			}
			got := stdout.String()
			if !strings.Contains(got, "ABC-123") {
				t.Errorf("%s output should carry the claim id, got:\n%s", format, got)
			}
			if strings.Contains(got, "Run 'namecom") {
				t.Errorf("%s output must not contain the human hint, got:\n%s", format, got)
			}
		})
	}
}

func TestDomainClaims_APIError(t *testing.T) {
	srv, _ := jsonServer(t, http.StatusNotFound, `{"message":"Domain not found"}`)
	cmd := cmdWithOutput(t, srv, tableOut(&bytes.Buffer{}))

	err := runClaims(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected an error for a 404 response, got nil")
	}
	if !strings.Contains(err.Error(), "Domain not found") {
		t.Errorf("error should surface the API message, got: %v", err)
	}
}
