package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/spf13/pflag"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/cmd/dns"
	"github.com/patramsey/namecom-cli/internal/output"
)

// ctrlC, given as an answer, stands for the user pressing Ctrl-C at that
// prompt: the form run that reads it reports huh.ErrUserAborted.
const ctrlC = "^C"

// formAnswers hands out one line per Read. huh's accessible mode wraps the
// reader in a fresh bufio.Scanner for every field, and a scanner given the
// whole input at once would swallow the answers meant for the fields after it.
type formAnswers struct {
	lines   []string
	aborted bool
}

func (r *formAnswers) Read(p []byte) (int, error) {
	if r.aborted || len(r.lines) == 0 {
		return 0, io.EOF
	}
	if r.lines[0] == ctrlC {
		r.lines, r.aborted = r.lines[1:], true
		return 0, io.EOF
	}
	n := copy(p, r.lines[0]+"\n")
	r.lines = r.lines[1:]
	return n, nil
}

// answerDNSForm runs the guided `dns create` form in huh's accessible
// (line-based) mode, reading answers from lines.
func answerDNSForm(t *testing.T, lines ...string) {
	t.Helper()
	in := &formAnswers{lines: lines}
	t.Cleanup(dns.StubFormRunner(func(f *huh.Form) error {
		err := f.WithAccessible(true).WithInput(in).WithOutput(io.Discard).Run()
		if in.aborted {
			return huh.ErrUserAborted
		}
		return err
	}))
	t.Cleanup(func() {
		if len(in.lines) != 0 {
			t.Errorf("form did not ask for every answer given; unread: %q", in.lines)
		}
	})
}

// resetDNSCreateFlags returns `dns create`'s flags to their defaults. They
// are bound to package variables on the one rootCmd every test shares, so a
// value or a Changed mark from an earlier run would otherwise carry over.
func resetDNSCreateFlags(t *testing.T) {
	t.Helper()
	c, _, err := rootCmd.Find([]string{"dns", "create"})
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		c.Flags().VisitAll(func(f *pflag.Flag) {
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		})
	}
	reset()
	t.Cleanup(reset)
}

// runDNSCreate runs `namecom --base-url <srv> dns create <args>` through the
// real root, interactive or not, and returns what it wrote to stderr and its
// error, classified as Execute classifies it.
func runDNSCreate(t *testing.T, srv *httptest.Server, interactive bool, args ...string) (string, error) {
	t.Helper()
	withConfig(t, loneProfile)
	resetDNSCreateFlags(t)
	t.Cleanup(output.StubInteractive(interactive))

	dir := t.TempDir()
	errFile, err := os.Create(filepath.Join(dir, "stderr")) //nolint:gosec // a path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devnull, errFile
	restore := func() { os.Stdout, os.Stderr = stdout, stderr }
	t.Cleanup(func() { restore(); _ = errFile.Close(); _ = devnull.Close() })

	prev := gf
	t.Cleanup(func() { gf = prev; rootCmd.SetArgs(nil) })
	rootCmd.SetArgs(append([]string{"--base-url", srv.URL, "--color", "never", "dns", "create"}, args...))
	runErr := cmdutil.ClassifyCobraUsage(rootCmd.ExecuteContext(context.Background()))
	restore()

	data, err := os.ReadFile(errFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data), runErr
}

// recordServer answers a record create and captures its body; any other
// request fails the test.
func recordServer(t *testing.T, sent *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/core/v1/domains/example.com/records" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(sent); err != nil {
			t.Errorf("decoding body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":7,"domainName":"example.com","host":"","fqdn":"example.com.","type":"MX","answer":"mx.example.com","ttl":3600,"priority":10}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestDNSCreate_FormReachableFromRoot pins #230: --type and --answer were
// marked required, so cobra rejected `dns create D` in a terminal before the
// guided form could run. Earlier tests called the form directly and never
// noticed.
func TestDNSCreate_FormReachableFromRoot(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		answers []string
		want    map[string]any
	}{
		{
			name: "no flags",
			// Type (5 = MX), host, answer, TTL; then the priority form.
			answers: []string{"5", "@", "mx.example.com", "3600", "10"},
			want:    map[string]any{"type": "MX", "host": "@", "answer": "mx.example.com", "ttl": float64(3600), "priority": float64(10)},
		},
		{
			name: "out-of-range and non-numeric priority asked again",
			answers: []string{"5", "@", "mx.example.com", "3600",
				"70000", "-1", "ten", "10"},
			want: map[string]any{"type": "MX", "host": "@", "answer": "mx.example.com", "ttl": float64(3600), "priority": float64(10)},
		},
		{
			name: "invalid host and answer asked again",
			// "bad host!" and "not-an-ip" fail ValidDNSHost / ValidDNSAnswer.
			answers: []string{"1", "bad host!", "www", "not-an-ip", "192.0.2.1", "300"},
			want:    map[string]any{"type": "A", "host": "www", "answer": "192.0.2.1", "ttl": float64(300)},
		},
		{
			name: "IDN host converted",
			// asciiHost's punycode conversion (#220) applies to the form too.
			answers: []string{"1", "bücher", "192.0.2.1", "300"},
			want:    map[string]any{"type": "A", "host": "xn--bcher-kva", "answer": "192.0.2.1", "ttl": float64(300)},
		},
		{
			name: "only --type given",
			args: []string{"--type", "A"},
			// The select defaults to the --type given; an empty line keeps it.
			answers: []string{"", "www", "192.0.2.1", "300"},
			want:    map[string]any{"type": "A", "host": "www", "answer": "192.0.2.1", "ttl": float64(300)},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var sent map[string]any
			srv := recordServer(t, &sent)
			answerDNSForm(t, tc.answers...)
			stderr, err := runDNSCreate(t, srv, true, append([]string{"example.com"}, tc.args...)...)
			if err != nil {
				t.Fatalf("dns create via the form: %v\nstderr: %s", err, stderr)
			}
			if !reflect.DeepEqual(sent, tc.want) {
				t.Errorf("form sent %v, want %v", sent, tc.want)
			}
		})
	}
}

// TestDNSCreate_FormCtrlCAtEveryStep pins #230: Ctrl-C at the MX priority
// step was ignored, and the record was created without a priority. Ctrl-C at
// any step must send nothing, print "aborted", and exit 0, as declining a
// confirmation does.
func TestDNSCreate_FormCtrlCAtEveryStep(t *testing.T) {
	steps := map[string][]string{
		"type":     {ctrlC},
		"host":     {"5", ctrlC},
		"answer":   {"5", "@", ctrlC},
		"ttl":      {"5", "@", "mx.example.com", ctrlC},
		"priority": {"5", "@", "mx.example.com", "3600", ctrlC},
	}
	for step, answers := range steps {
		t.Run(step, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				t.Errorf("Ctrl-C at %s still sent %s %s", step, r.Method, r.URL.Path)
			}))
			t.Cleanup(srv.Close)
			answerDNSForm(t, answers...)
			stderr, err := runDNSCreate(t, srv, true, "example.com")
			if err != nil {
				t.Fatalf("Ctrl-C at %s: got error %v, want exit 0", step, err)
			}
			if !strings.Contains(stderr, "aborted") {
				t.Errorf("Ctrl-C at %s: stderr %q does not say aborted", step, stderr)
			}
		})
	}
}

// TestDNSCreate_NonInteractiveMissingFlags: with the required marks gone,
// a missing --type or --answer without a terminal must still be a usage
// error (exit 2), not a prompt nobody can answer.
func TestDNSCreate_NonInteractiveMissingFlags(t *testing.T) {
	for _, args := range [][]string{
		{"example.com"},
		{"example.com", "--type", "A"},
		{"example.com", "--answer", "192.0.2.1"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				t.Errorf("sent %s %s", r.Method, r.URL.Path)
			}))
			t.Cleanup(srv.Close)
			t.Cleanup(dns.StubFormRunner(func(*huh.Form) error {
				t.Error("the form ran without a terminal")
				return nil
			}))
			_, err := runDNSCreate(t, srv, false, args...)
			if got := exitCode(err); got != 2 {
				t.Errorf("exit %d (%v), want 2", got, err)
			}
			if err == nil || !strings.Contains(err.Error(), "required flag") {
				t.Errorf("error %v does not name the missing flag", err)
			}
		})
	}
}

// TestDNSCreate_RequiredFlagHelp: the flags are no longer marked required,
// so their help says so instead, and when they are asked for.
func TestDNSCreate_RequiredFlagHelp(t *testing.T) {
	c, _, err := rootCmd.Find([]string{"dns", "create"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"type", "answer"} {
		if u := c.Flags().Lookup(name).Usage; !strings.Contains(u, "(required; prompted in a terminal)") {
			t.Errorf("--%s help %q does not say it is required and prompted", name, u)
		}
	}
}
