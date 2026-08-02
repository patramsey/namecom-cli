package cmd

import (
	"strings"
	"testing"
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
