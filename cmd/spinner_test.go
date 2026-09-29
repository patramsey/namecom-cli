package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/internal/output"
)

// Response bodies trimmed from ones captured against the name.com sandbox.
const (
	spinDNSList    = `{"totalCount":2,"from":1,"to":2,"records":[{"answer":"192.0.2.1","domainName":"example.com","fqdn":"www.example.com.","host":"www","id":13518084,"ttl":300,"type":"A"},{"answer":"mx6.name.com","domainName":"example.com","fqdn":"example.com.","host":"","id":13518104,"priority":10,"ttl":300,"type":"MX"}]}`
	spinDNSRecord  = `{"answer":"192.0.2.1","domainName":"example.com","fqdn":"www.example.com.","host":"www","id":42,"ttl":300,"type":"A"}`
	spinDomain     = `{"domainName":"example.com","createDate":"2026-09-29T05:37:38Z","expireDate":"2027-09-29T05:37:38Z","autorenewEnabled":false,"locked":true,"privacyEnabled":false,"nameservers":["ns1vwx.name.com","ns2gtx.name.com"],"renewalPrice":19.99}`
	spinEmailList  = `{"emailForwarding":[{"domainName":"example.com","emailBox":"info","emailTo":"smoke2@example.net"}]}`
	spinURLList    = `{"urlForwarding":[{"domainName":"example.com","forwardsTo":"https:\/\/example.com\/a","host":"@","type":"redirect","id":37187}]}`
	spinURL        = `{"domainName":"example.com","forwardsTo":"https:\/\/example.com\/a","host":"www","type":"redirect","id":37187}`
	spinNotFound   = `{"message":"Not Found","details":"not found"}`
	spinValidation = `{"message":"Invalid Argument","details":"Parameter Value Error - Invalid Answer"}`
)

// spinServer answers every request with status and, when status is 200, the
// body chosen by path.
func spinServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case status == http.StatusNotFound:
			w.WriteHeader(status)
			_, _ = w.Write([]byte(spinNotFound))
			return
		case status != http.StatusOK:
			w.WriteHeader(status)
			_, _ = w.Write([]byte(spinValidation))
			return
		}
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/records") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(spinDNSList))
		case strings.Contains(p, "/records"):
			_, _ = w.Write([]byte(spinDNSRecord))
		case strings.HasSuffix(p, "/email/forwarding"):
			_, _ = w.Write([]byte(spinEmailList))
		case strings.HasSuffix(p, "/url/forwarding") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(spinURLList))
		case strings.Contains(p, "/url/forwarding"):
			_, _ = w.Write([]byte(spinURL))
		default:
			_, _ = w.Write([]byte(spinDomain))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// runWithSpinners runs `namecom <args> -o table` through the real root, with
// spinners enabled and recorded, and returns the recorder and the command's
// error. Table mode is where spinners run; stdout and stderr are discarded.
func runWithSpinners(t *testing.T, srv *httptest.Server, args []string) (*output.SpinRecorder, error) {
	t.Helper()
	rec, restore := output.RecordSpinners()
	t.Cleanup(restore)
	t.Cleanup(output.StubInteractive(false))

	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening %s: %v", os.DevNull, err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devnull, devnull
	t.Cleanup(func() { os.Stdout, os.Stderr = stdout, stderr; _ = devnull.Close() })

	prev := gf
	t.Cleanup(func() { gf = prev; rootCmd.SetArgs(nil) })
	rootCmd.SetArgs(append([]string{"--base-url", srv.URL, "--yes", "-o", "table"}, args...))
	return rec, rootCmd.ExecuteContext(context.Background())
}

// TestSpinner_NotStartedForInvalidDomain pins the fixes from #97: these get
// commands validate their domain argument before starting a spinner, so an
// invalid one fails without leaving a spinner drawing over the error.
func TestSpinner_NotStartedForInvalidDomain(t *testing.T) {
	withConfig(t, loneProfile)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s: an invalid domain must fail before any API call", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	for _, args := range [][]string{
		{"email", "get", "not a domain", "info"},
		{"url", "get", "not a domain", "1"},
		{"vanity-ns", "get", "not a domain", "ns1"},
		{"dnssec", "get", "not a domain", "ABCDEF"},
	} {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			rec, err := runWithSpinners(t, srv, args)
			if err == nil {
				t.Fatalf("namecom %s succeeded; want an invalid-domain error", strings.Join(args, " "))
			}
			if got := rec.Started(); len(got) != 0 {
				t.Errorf("spinner started before the domain was validated: %v", got)
			}
		})
	}
}

// TestSpinner_AlwaysStopped runs a spread of commands — reads and writes, on
// success and on an API error — with spinners enabled, and asserts that every
// spinner a command starts is stopped by the time it returns. A spinner left
// running keeps redrawing over whatever the command prints next, including
// the error. Each case must start at least one spinner, or it tests nothing.
func TestSpinner_AlwaysStopped(t *testing.T) {
	withConfig(t, loneProfile)

	commands := [][]string{
		{"dns", "list", "example.com"},
		{"url", "create", "example.com", "--host", "www", "--to", "https://example.com/a"},
		{"dns", "delete", "example.com", "42"},
		{"domain", "get", "example.com"},
		{"email", "list", "example.com"},
		{"url", "list", "example.com"},
	}
	errorOnly := [][]string{
		{"email", "get", "example.com", "info"},
		{"url", "get", "example.com", "37187"},
		{"vanity-ns", "get", "example.com", "ns1"},
		{"dnssec", "get", "example.com", "ABCDEF"},
	}

	type run struct {
		name    string
		status  int
		args    []string
		wantErr bool
	}
	var runs []run
	for _, args := range commands {
		runs = append(runs,
			run{"ok/" + strings.Join(args[:2], " "), http.StatusOK, args, false},
			run{"404/" + strings.Join(args[:2], " "), http.StatusNotFound, args, true})
	}
	for _, args := range errorOnly {
		runs = append(runs, run{"404/" + strings.Join(args[:2], " "), http.StatusNotFound, args, true})
	}
	runs = append(runs, run{"422/url create", http.StatusUnprocessableEntity, commands[1], true})

	for _, tc := range runs {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := runWithSpinners(t, spinServer(t, tc.status), tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("namecom %s: err = %v, wantErr %v", strings.Join(tc.args, " "), err, tc.wantErr)
			}
			if len(rec.Started()) == 0 {
				t.Fatalf("namecom %s started no spinner, so this case checks nothing", strings.Join(tc.args, " "))
			}
			if running := rec.Running(); len(running) != 0 {
				t.Errorf("namecom %s returned with spinner(s) still running: %q", strings.Join(tc.args, " "), running)
			}
		})
	}
}
