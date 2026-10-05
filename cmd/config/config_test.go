package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// secretToken is a distinctive value so any leak is unmistakable in output.
const secretToken = "SUPERSECRET-TOKEN-VALUE"

// configCmd writes a config file containing a real-looking token, points the
// CLI at it via NAMECOM_CONFIG, and returns a command whose output lands in the
// returned buffer.
func configCmd(t *testing.T, format output.Format) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := "default: prod\nprofiles:\n  prod:\n    username: alice\n    token: " + secretToken + "\n" +
		"  helper:\n    username: bob\n    token_cmd: op read op://vault/namecom/token\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	t.Setenv("NAMECOM_CONFIG", path)
	// Neutralize ambient profile selection: runShow honours NAMECOM_PROFILE, so
	// a developer with it exported saw these tests fail against a profile the
	// fixture never defines.
	t.Setenv("NAMECOM_PROFILE", "")

	var buf bytes.Buffer
	out := &output.Config{Format: format, Color: output.ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	cmd.SetContext(context.WithValue(context.Background(), cmdutil.KeyOutput, out))
	return cmd, &buf
}

// TestShow_InvalidSandboxEnv guards #225: config show describes the endpoint
// an API command would use, and an unrecognized NAMECOM_SANDBOX was read as
// production. It is now the same usage error the API commands report.
func TestShow_InvalidSandboxEnv(t *testing.T) {
	cmd, buf := configCmd(t, output.FormatJSON)
	t.Setenv("NAMECOM_SANDBOX", "garbage")
	err := runShow(cmd, nil)
	if err == nil {
		t.Fatalf("config show accepted NAMECOM_SANDBOX=garbage:\n%s", buf.String())
	}
	if _, ok := errors.AsType[*cmdutil.UsageError](err); !ok {
		t.Errorf("error is %T, want a UsageError (exit 2): %v", err, err)
	}
	if strings.Contains(buf.String(), "api.name.com") {
		t.Errorf("config show still reported production:\n%s", buf.String())
	}
}

// TestListProfiles_NeverLeaksToken is a credential-disclosure guard.
// `config list-profiles` serialized config.Profile directly, and Profile.Token
// carries no `json:"-"`. Because DefaultConfig() selects JSON whenever stdout
// is not a TTY, `namecom config list-profiles | anything` wrote every profile's
// live token to stdout — into pipes, CI logs, and shell redirects. The table
// path never showed tokens, so the leak was clearly unintended.
func TestListProfiles_NeverLeaksToken(t *testing.T) {
	for _, format := range []output.Format{output.FormatJSON, output.FormatYAML, output.FormatTable} {
		t.Run(string(format), func(t *testing.T) {
			cmd, buf := configCmd(t, format)
			if err := runListProfiles(cmd, nil); err != nil {
				t.Fatalf("runListProfiles: %v", err)
			}
			got := buf.String()
			if strings.Contains(got, secretToken) {
				t.Errorf("CREDENTIAL LEAK: token appeared in %s output:\n%s", format, got)
			}
			// token_cmd can itself embed a secret (vault paths, arguments).
			if strings.Contains(got, "op://vault/namecom/token") {
				t.Errorf("token_cmd leaked in %s output:\n%s", format, got)
			}
			// The command must still be useful: profile names have to appear.
			if !strings.Contains(got, "prod") {
				t.Errorf("profile names missing from %s output:\n%s", format, got)
			}
		})
	}
}

// TestShow_DoesNotExposeTokenCmdArguments covers a subtler leak than a raw
// token: a token_cmd's *arguments* can themselves carry a secret (an inline
// bearer token, a vault path). `config show` printed the whole command, so it
// could appear on a shared screen or in a screenshot. Naming the helper program
// answers "where does my token come from?" without echoing its arguments.
func TestShow_DoesNotExposeTokenCmdArguments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := "default: helper\nprofiles:\n  helper:\n    username: bob\n" +
		`    token_cmd: "curl -H 'Authorization: Bearer INLINE-SECRET' https://vault.example/token"` + "\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	t.Setenv("NAMECOM_CONFIG", path)
	// Neutralize ambient profile selection: runShow honours NAMECOM_PROFILE, so
	// a developer with it exported saw these tests fail against a profile the
	// fixture never defines.
	t.Setenv("NAMECOM_PROFILE", "")

	var buf bytes.Buffer
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	cmd.SetContext(context.WithValue(context.Background(), cmdutil.KeyOutput, out))

	if err := runShow(cmd, nil); err != nil {
		t.Fatalf("runShow: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "INLINE-SECRET") {
		t.Errorf("token_cmd arguments leaked a secret:\n%s", got)
	}
	if !strings.Contains(got, "token_cmd") {
		t.Errorf("output should still say the token comes from a token_cmd:\n%s", got)
	}
	if !strings.Contains(got, "curl") {
		t.Errorf("output should still name the helper program:\n%s", got)
	}
}

// showCmdFor writes a config whose DEFAULT profile carries both a literal token
// and a token_cmd, then returns a command rendering into the returned buffer.
//
// Both credentials must live on the *default* profile, because that is the only
// one runShow reads. The previous fixture put token_cmd on a second profile, so
// the assertion guarding it inspected data the command never touched.
func showCmdFor(t *testing.T, format output.Format, overrideProfile string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := "default: prod\nprofiles:\n" +
		"  prod:\n    username: alice\n    token: " + secretToken + "\n" +
		`    token_cmd: "op read op://vault/namecom/token"` + "\n" +
		"  sandy:\n    username: bob\n    token: " + secretToken + "-SANDY\n    sandbox: true\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	t.Setenv("NAMECOM_CONFIG", path)
	t.Setenv("NAMECOM_PROFILE", "")

	var buf bytes.Buffer
	out := &output.Config{Format: format, Color: output.ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	if overrideProfile != "" {
		// The --profile flag reaches commands as resolved Overrides on the
		// context, which is the path root.go populates.
		ctx = context.WithValue(ctx, cmdutil.KeyOverrides, config.Overrides{Profile: overrideProfile})
	}
	cmd.SetContext(ctx)
	return cmd, &buf
}

// TestShow_NeverLeaksToken guards `config show` against disclosing the
// credential of the profile it is describing.
//
// The earlier version could not meaningfully fail: its fixture's default
// profile had no token_cmd, so the assertion guarding token_cmd inspected a
// profile runShow never reads. Here the default profile carries BOTH a literal
// token and a token_cmd, so both leak paths are live in every output format.
func TestShow_NeverLeaksToken(t *testing.T) {
	for _, format := range []output.Format{output.FormatJSON, output.FormatYAML, output.FormatTable} {
		t.Run(string(format), func(t *testing.T) {
			cmd, buf := showCmdFor(t, format, "")
			if err := runShow(cmd, nil); err != nil {
				t.Fatalf("runShow: %v", err)
			}
			got := buf.String()

			if strings.Contains(got, secretToken) {
				t.Errorf("CREDENTIAL LEAK: the token appeared in %s output:\n%s", format, got)
			}
			// A token_cmd's arguments can themselves be a secret (a vault path,
			// an inline bearer token), so the full command must not be echoed.
			if strings.Contains(got, "op://vault/namecom/token") {
				t.Errorf("token_cmd arguments leaked in %s output:\n%s", format, got)
			}
			// The command must still be useful — it has to say WHICH profile it
			// is describing, or the redaction has cost all the value.
			if !strings.Contains(got, "prod") {
				t.Errorf("%s output should still identify the profile:\n%s", format, got)
			}
		})
	}
}

// TestShow_HonorsProfileSelection guards a wrong-answer bug: runShow read
// cfgFile.Default only, so `config show --profile sandbox` — the command's own
// documented example — reported the production profile's endpoint.
//
// The earlier version only set NAMECOM_PROFILE and never seeded the Overrides
// the --profile flag actually travels on, so the flag branch it was named for
// was untested; blanking that branch left it green.
func TestShow_HonorsProfileSelection(t *testing.T) {
	t.Run("--profile flag", func(t *testing.T) {
		cmd, buf := showCmdFor(t, output.FormatJSON, "sandy")
		if err := runShow(cmd, nil); err != nil {
			t.Fatalf("runShow: %v", err)
		}
		assertDescribesSandy(t, buf.String())
	})

	t.Run("NAMECOM_PROFILE env var", func(t *testing.T) {
		cmd, buf := showCmdFor(t, output.FormatJSON, "")
		t.Setenv("NAMECOM_PROFILE", "sandy")
		if err := runShow(cmd, nil); err != nil {
			t.Fatalf("runShow: %v", err)
		}
		assertDescribesSandy(t, buf.String())
	})

	t.Run("flag beats env", func(t *testing.T) {
		// Documented precedence: flag > env > profile > default.
		cmd, buf := showCmdFor(t, output.FormatJSON, "sandy")
		t.Setenv("NAMECOM_PROFILE", "prod")
		if err := runShow(cmd, nil); err != nil {
			t.Fatalf("runShow: %v", err)
		}
		assertDescribesSandy(t, buf.String())
	})

	t.Run("falls back to the file default", func(t *testing.T) {
		cmd, buf := showCmdFor(t, output.FormatJSON, "")
		if err := runShow(cmd, nil); err != nil {
			t.Fatalf("runShow: %v", err)
		}
		got := buf.String()
		if !strings.Contains(got, "prod") || !strings.Contains(got, "api.name.com") {
			t.Errorf("with no selection the file default should be described, got: %s", got)
		}
	})
}

// TestShow_MatchesResolve covers the cases where config show's own chain
// disagreed with the credentials API commands use: a lone profile with no
// `default:` key (every API command worked; config show said "no profile
// \"default\" configured — run 'namecom auth login'", which overwrites), and
// the env/flag overrides that change the username and endpoint.
func TestShow_MatchesResolve(t *testing.T) {
	writeConfig := func(t *testing.T, contents string) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatalf("writing config: %v", err)
		}
		t.Setenv("NAMECOM_CONFIG", path)
		for _, k := range []string{"NAMECOM_PROFILE", "NAMECOM_USERNAME", "NAMECOM_TOKEN", "NAMECOM_SANDBOX", "NAMECOM_BASE_URL"} {
			t.Setenv(k, "")
		}
	}
	run := func(t *testing.T, ov config.Overrides) map[string]string {
		t.Helper()
		var buf bytes.Buffer
		out := &output.Config{Format: output.FormatJSON, Color: output.ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}}
		cmd := &cobra.Command{}
		ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
		ctx = context.WithValue(ctx, cmdutil.KeyOverrides, ov)
		cmd.SetContext(ctx)
		if err := runShow(cmd, nil); err != nil {
			t.Fatalf("runShow: %v", err)
		}
		var got map[string]string
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("parsing output: %v\n%s", err, buf.String())
		}
		return got
	}

	t.Run("lone profile not named default", func(t *testing.T) {
		writeConfig(t, "profiles:\n  work:\n    username: w\n    token: x\n")
		got := run(t, config.Overrides{})
		if got["profile"] != "work" || got["username"] != "w" {
			t.Errorf("got %v, want profile work / username w", got)
		}
	})

	prod := "default: prod\nprofiles:\n  prod:\n    username: alice\n    token: x\n"

	t.Run("--sandbox", func(t *testing.T) {
		writeConfig(t, prod)
		if got := run(t, config.Overrides{Sandbox: true, SandboxSet: true}); got["endpoint"] != "https://api.dev.name.com" {
			t.Errorf("endpoint = %q with --sandbox, want https://api.dev.name.com", got["endpoint"])
		}
	})

	t.Run("NAMECOM_SANDBOX", func(t *testing.T) {
		writeConfig(t, prod)
		t.Setenv("NAMECOM_SANDBOX", "true")
		if got := run(t, config.Overrides{}); got["endpoint"] != "https://api.dev.name.com" {
			t.Errorf("endpoint = %q with NAMECOM_SANDBOX, want https://api.dev.name.com", got["endpoint"])
		}
	})

	t.Run("NAMECOM_USERNAME", func(t *testing.T) {
		writeConfig(t, prod)
		t.Setenv("NAMECOM_USERNAME", "envuser")
		if got := run(t, config.Overrides{}); got["username"] != "envuser" {
			t.Errorf("username = %q with NAMECOM_USERNAME, want envuser", got["username"])
		}
	})
}

// assertDescribesSandy checks the output describes the sandbox profile — both
// its name and its endpoint, since reporting the wrong endpoint is the concrete
// harm (a user believing they are pointed at sandbox when they are not).
func assertDescribesSandy(t *testing.T, got string) {
	t.Helper()
	if !strings.Contains(got, "sandy") {
		t.Errorf("expected the selected profile 'sandy', got: %s", got)
	}
	if !strings.Contains(got, "api.dev.name.com") {
		t.Errorf("expected the sandbox endpoint for a sandbox profile, got: %s", got)
	}
	if strings.Contains(got, "api.name.com\"") {
		t.Errorf("reported the production endpoint for a sandbox profile: %s", got)
	}
}

// TestShow_EndpointIncludesScheme pins #135: config show printed the endpoint
// as a bare host while auth status printed the base URL, so the same value
// appeared in two forms. Both now print the URL the client talks to.
func TestShow_EndpointIncludesScheme(t *testing.T) {
	for _, tc := range []struct{ profile, want string }{
		{"prod", "https://api.name.com"},
		{"sandy", "https://api.dev.name.com"},
	} {
		for _, format := range []output.Format{output.FormatJSON, output.FormatYAML, output.FormatTable} {
			t.Run(tc.profile+"/"+string(format), func(t *testing.T) {
				cmd, buf := showCmdFor(t, format, tc.profile)
				if err := runShow(cmd, nil); err != nil {
					t.Fatalf("runShow: %v", err)
				}
				if !strings.Contains(buf.String(), tc.want) {
					t.Errorf("%s output should show endpoint %s:\n%s", format, tc.want, buf.String())
				}
			})
		}
	}
}

// TestListProfiles_MarksActiveProfile pins #129: list-profiles marked the
// profile named by the file's `default:` key, while every API command, `config
// show` and `auth status` use config.ActiveProfile. With NAMECOM_PROFILE set it
// marked a different profile than the one in use, and with one profile and no
// `default:` key it marked none.
func TestListProfiles_MarksActiveProfile(t *testing.T) {
	two := "default: prod\nprofiles:\n  prod:\n    username: alice\n    token: x\n" +
		"  staging:\n    username: bob\n    token: y\n"
	cases := []struct {
		name     string
		contents string
		env      string
		flag     string
		want     string
	}{
		{name: "explicit default key", contents: two, want: "prod"},
		{name: "NAMECOM_PROFILE beats default key", contents: two, env: "staging", want: "staging"},
		{name: "--profile beats NAMECOM_PROFILE", contents: two, env: "prod", flag: "staging", want: "staging"},
		{name: "lone profile, no default key", contents: "profiles:\n  work:\n    username: w\n    token: x\n", want: "work"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.contents), 0o600); err != nil {
				t.Fatalf("writing config: %v", err)
			}
			t.Setenv("NAMECOM_CONFIG", path)
			t.Setenv("NAMECOM_PROFILE", tc.env)

			var buf bytes.Buffer
			out := &output.Config{Format: output.FormatJSON, Color: output.ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}}
			cmd := &cobra.Command{}
			ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
			ctx = context.WithValue(ctx, cmdutil.KeyOverrides, config.Overrides{Profile: tc.flag})
			cmd.SetContext(ctx)
			if err := runListProfiles(cmd, nil); err != nil {
				t.Fatalf("runListProfiles: %v", err)
			}

			var views []profileView
			if err := unmarshalData(buf.Bytes(), &views); err != nil {
				t.Fatalf("parsing output: %v\n%s", err, buf.String())
			}
			var marked []string
			for _, v := range views {
				if v.Default {
					marked = append(marked, v.Name)
				}
			}
			if len(marked) != 1 || marked[0] != tc.want {
				t.Errorf("marked %v, want exactly [%s]", marked, tc.want)
			}
		})
	}
}

// TestListProfiles_EndpointIncludesScheme pins #187: #135 made config show
// print the endpoint as a URL, but list-profiles kept the bare host.
func TestListProfiles_EndpointIncludesScheme(t *testing.T) {
	for _, format := range []output.Format{output.FormatJSON, output.FormatYAML, output.FormatTable} {
		t.Run(string(format), func(t *testing.T) {
			cmd, buf := showCmdFor(t, format, "")
			if err := runListProfiles(cmd, nil); err != nil {
				t.Fatalf("runListProfiles: %v", err)
			}
			for _, want := range []string{"https://api.name.com", "https://api.dev.name.com"} {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("%s output should show endpoint %s:\n%s", format, want, buf.String())
				}
			}
		})
	}
}

// unmarshalData decodes the {"data": [...]} envelope every list prints in
// JSON mode (#240) into v.
func unmarshalData(b []byte, v any) error {
	var doc struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return err
	}
	if doc.Data == nil {
		return errors.New(`no "data" key`)
	}
	return json.Unmarshal(doc.Data, v)
}
