package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/internal/output"
)

// openTarget's result is handed to `open`/`xdg-open`/`rundll32` as an argv
// element. These assert the two properties that keeps safe: the argument is a
// name.com URL, and nothing an argument-parser would read as a flag survives.
func TestOpenTarget(t *testing.T) {
	const dashboard = "https://www.name.com/account/domain/"

	tests := []struct {
		name    string
		args    []string
		want    string
		wantErr bool
	}{
		{
			name: "no argument opens the dashboard",
			args: nil,
			want: dashboard,
		},
		{
			name: "domain opens its details page",
			args: []string{"acme.io"},
			want: dashboard + "details#?domain=acme.io",
		},
		{
			// CanonicalDomain lowercases and trims; it does not strip a scheme.
			name: "domain is lowercased and trimmed",
			args: []string{"  ACME.IO  "},
			want: dashboard + "details#?domain=acme.io",
		},
		{
			// The finding that motivated the validation: `open -foo` passes
			// -foo to open(1) as a flag rather than opening anything.
			name:    "leading dash is rejected",
			args:    []string{"-foo"},
			wantErr: true,
		},
		{
			name:    "leading dash before a real domain is rejected",
			args:    []string{"--version acme.io"},
			wantErr: true,
		},
		{
			name:    "empty argument is rejected",
			args:    []string{""},
			wantErr: true,
		},
		{
			name:    "path traversal is rejected",
			args:    []string{"../../etc/passwd"},
			wantErr: true,
		},
		{
			name:    "whitespace is rejected",
			args:    []string{"acme.io evil.com"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := openTarget(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("openTarget(%q) = %q, want error", tt.args, got)
				}
				if got != "" {
					t.Errorf("openTarget(%q) returned %q alongside an error, want empty", tt.args, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("openTarget(%q) unexpected error: %v", tt.args, err)
			}
			if got != tt.want {
				t.Errorf("openTarget(%q) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

// Whatever openTarget returns must be unambiguously a URL to the argument
// parser on the other side, for every input that reaches it.
func TestOpenTargetNeverYieldsAFlag(t *testing.T) {
	inputs := []string{
		"", "-", "--", "-e", "-a/Applications/Calculator.app", "--args",
		"acme.io", "-acme.io", "acme.io -e", "/etc/passwd", "file:///etc/passwd",
		"javascript:alert(1)", "acme.io#-x", "acme.io?-x", "acme.io\n-x",
	}
	for _, in := range inputs {
		got, err := openTarget([]string{in})
		if err != nil {
			continue // rejected outright, which is fine
		}
		if !strings.HasPrefix(got, "https://www.name.com/") {
			t.Errorf("openTarget(%q) = %q, want a https://www.name.com/ URL", in, got)
		}
		if strings.ContainsAny(got, " \t\n\r") {
			t.Errorf("openTarget(%q) = %q, contains whitespace that could split the argument", in, got)
		}
	}
}

// stubStart replaces startCommand for the test, recording every argv it is
// handed, prefixed with "wait" when it would run in the foreground, and
// answering with fail(name). No test in this package may reach the
// real exec.Command: it would open a browser on the developer's machine.
func stubStart(t *testing.T, fail func(name string) error) *[][]string {
	t.Helper()
	var calls [][]string
	prev := startCommand
	startCommand = func(wait bool, name string, args ...string) error {
		call := append([]string{name}, args...)
		if wait {
			call = append([]string{"wait"}, call...)
		}
		calls = append(calls, call)
		return fail(name)
	}
	t.Cleanup(func() { startCommand = prev })
	return &calls
}

func openOut(format output.Format) (*output.Config, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	return &output.Config{Format: format, Color: output.ColorNever, Writer: &stdout, EWriter: &stderr}, &stdout, &stderr
}

// TestOpenBrowser_HonoursBROWSER guards issue #184: $BROWSER is the standard
// way to name a browser where there is no desktop opener, and it was ignored.
// It is a list of commands separated like PATH; %s marks where the URL goes,
// and without one the URL is appended.
func TestOpenBrowser_HonoursBROWSER(t *testing.T) {
	const target = "https://www.name.com/account/domain/"
	sep := string(os.PathListSeparator)

	t.Run("first command that starts wins", func(t *testing.T) {
		t.Setenv("BROWSER", "missing-browser"+sep+"w3m -o x")
		calls := stubStart(t, func(name string) error {
			if name == "missing-browser" {
				return exec.ErrNotFound
			}
			return nil
		})
		if err := openBrowser(target); err != nil {
			t.Fatalf("openBrowser: %v", err)
		}
		want := [][]string{{"wait", "missing-browser", target}, {"wait", "w3m", "-o", "x", target}}
		if !reflect.DeepEqual(*calls, want) {
			t.Errorf("calls = %q, want %q", *calls, want)
		}
	})

	t.Run("%s places the URL", func(t *testing.T) {
		t.Setenv("BROWSER", "lynx --url=%s --flag")
		calls := stubStart(t, func(string) error { return nil })
		if err := openBrowser(target); err != nil {
			t.Fatalf("openBrowser: %v", err)
		}
		want := [][]string{{"wait", "lynx", "--url=" + target, "--flag"}}
		if !reflect.DeepEqual(*calls, want) {
			t.Errorf("calls = %q, want %q", *calls, want)
		}
	})

	t.Run("falls back to the platform opener", func(t *testing.T) {
		t.Setenv("BROWSER", "missing-browser")
		calls := stubStart(t, func(name string) error {
			if name == "missing-browser" {
				return exec.ErrNotFound
			}
			return nil
		})
		if err := openBrowser(target); err != nil {
			t.Fatalf("openBrowser: %v", err)
		}
		if len(*calls) != 2 {
			t.Fatalf("calls = %q, want $BROWSER then the platform opener", *calls)
		}
		// The desktop opener hands off and returns; waiting on it is wrong.
		if (*calls)[1][0] == "wait" {
			t.Errorf("platform opener %q run in the foreground", (*calls)[1])
		}
	})

	t.Run("error when nothing starts", func(t *testing.T) {
		t.Setenv("BROWSER", "")
		stubStart(t, func(string) error { return exec.ErrNotFound })
		if err := openBrowser(target); err == nil {
			t.Error("openBrowser succeeded with no opener available")
		}
	})
}

// TestRenderOpen guards issue #184: with no opener (headless Linux, SSH,
// containers) `namecom open` failed with a raw exec error and exit 1, and in
// JSON mode the URL appeared nowhere. The URL is the useful result either way.
func TestRenderOpen(t *testing.T) {
	const target = "https://www.name.com/account/domain/"
	notFound := fmt.Errorf("exec: %q: %w", "xdg-open", exec.ErrNotFound)

	t.Run("json always carries the URL", func(t *testing.T) {
		for _, launchErr := range []error{nil, notFound} {
			out, stdout, stderr := openOut(output.FormatJSON)
			if err := renderOpen(out, target, launchErr); err != nil {
				t.Fatalf("renderOpen(%v): %v", launchErr, err)
			}
			var got struct {
				URL    string `json:"url"`
				Opened bool   `json:"opened"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
				t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
			}
			if got.URL != target || got.Opened != (launchErr == nil) {
				t.Errorf("launchErr=%v: got %+v", launchErr, got)
			}
			if stderr.Len() != 0 {
				t.Errorf("launchErr=%v: stderr not empty in JSON mode: %q", launchErr, stderr)
			}
		}
	})

	t.Run("table prints the URL to open by hand", func(t *testing.T) {
		out, stdout, stderr := openOut(output.FormatTable)
		if err := renderOpen(out, target, notFound); err != nil {
			t.Fatalf("renderOpen: %v", err)
		}
		if !strings.Contains(stdout.String(), "Open this URL in your browser: "+target) {
			t.Errorf("stdout = %q, want the URL and how to use it", stdout)
		}
		if !strings.Contains(stderr.String(), "xdg-open") {
			t.Errorf("stderr = %q, want the reason no browser opened", stderr)
		}
	})

	t.Run("table when opened", func(t *testing.T) {
		out, stdout, stderr := openOut(output.FormatTable)
		if err := renderOpen(out, target, nil); err != nil {
			t.Fatalf("renderOpen: %v", err)
		}
		// Commentary, so stderr; stdout stays empty.
		if !strings.Contains(stderr.String(), "Opening "+target) || stdout.Len() != 0 {
			t.Errorf("stdout = %q, stderr = %q", stdout, stderr)
		}
	})
}
