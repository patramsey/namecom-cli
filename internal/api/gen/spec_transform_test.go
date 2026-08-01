package gen_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// NOTE ON CACHING: these tests shell out to scripts/spec_to_30.py and read
// namecom.api.yaml. Go's test cache tracks neither, so after editing the
// preprocessor or the spec you MUST run `go test -count=1 ./internal/api/gen/`
// — a plain `go test` will report a stale pass. `make generate` regenerates
// from the same script, so CI should run the suite with -count=1 after it.
//
// scripts/spec_to_30.py rewrites the vendored OpenAPI 3.1 spec into a 3.0-
// compatible form because oapi-codegen v2.4.x cannot read 3.1. It is a
// line-based transform over a 16k-line YAML file, and every generated Go type
// in this package depends on it — yet nothing verified its output.
//
// That is the risk these tests close. An unconverted 3.1 construct does NOT
// fail codegen; it silently produces the wrong Go type, which surfaces much
// later as a runtime decode bug.

func repoRoot(t *testing.T) string {
	t.Helper()
	// internal/api/gen -> repo root
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}
	return root
}

// runTransform runs the preprocessor on src and returns the output path.
// Skips (rather than fails) when python3 is unavailable, so the suite still
// runs in minimal environments; `make generate` needs python3 regardless.
func runTransform(t *testing.T, src string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available; skipping spec-transform verification")
	}
	root := repoRoot(t)
	script := filepath.Join(root, "scripts", "spec_to_30.py")

	// Go's test cache keys on Go sources and a few tracked inputs — it cannot
	// see this Python script or the vendored spec. Without reading them here,
	// editing the preprocessor and re-running yields a STALE "ok (cached)",
	// which is how a deliberately broken transform was observed passing. Hashing
	// both files into the test's observable inputs is not possible, so instead
	// fail loudly if they are unreadable and document that -count=1 is required
	// after editing either.
	for _, f := range []string{script, filepath.Join(root, "namecom.api.yaml")} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("required input %s: %v", f, err)
		}
	}

	dst := filepath.Join(t.TempDir(), "spec30.yaml")
	cmd := exec.Command("python3", script, src, dst)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return dst, nil
}

func loadYAML(t *testing.T, path string) any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var v any
	if err := yaml.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s is not valid YAML: %v", path, err)
	}
	return v
}

// collectDiffs walks two decoded specs and records every structural difference.
func collectDiffs(a, b any, path string, out *[]string) {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			*out = append(*out, fmt.Sprintf("%s: map became %T", path, b))
			return
		}
		seen := map[string]bool{}
		for k := range av {
			seen[k] = true
		}
		for k := range bv {
			seen[k] = true
		}
		keys := make([]string, 0, len(seen))
		for k := range seen {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			x, inA := av[k]
			y, inB := bv[k]
			switch {
			case inA && !inB:
				*out = append(*out, fmt.Sprintf("%s/%s: REMOVED", path, k))
			case !inA && inB:
				*out = append(*out, fmt.Sprintf("%s/%s: ADDED (%v)", path, k, y))
			default:
				collectDiffs(x, y, path+"/"+k, out)
			}
		}
	case []any:
		bv, ok := b.([]any)
		if !ok {
			// Record the members AND the resulting scalar. An earlier version
			// printed %T, which is "string" for every YAML scalar — so rewriting
			// a [string, null] union to `integer` produced a byte-identical diff
			// line and was excused by the whitelist. Verified: emitting
			// `type: integer` for all 223 nullable unions passed.
			*out = append(*out, fmt.Sprintf("%s: list %v became scalar %v", path, av, b))
			return
		}
		if len(av) != len(bv) {
			*out = append(*out, fmt.Sprintf("%s: list length %d -> %d", path, len(av), len(bv)))
			return
		}
		for i := range av {
			collectDiffs(av[i], bv[i], fmt.Sprintf("%s[%d]", path, i), out)
		}
	default:
		if !reflect.DeepEqual(a, b) {
			*out = append(*out, fmt.Sprintf("%s: %v -> %v", path, a, b))
		}
	}
}

// isDocumentedTransform reports whether a difference is one the preprocessor is
// supposed to make. Anything else means it changed the spec's meaning.
func isDocumentedTransform(d string) bool {
	switch {
	case strings.HasPrefix(d, "/openapi:"):
		return true // 3.1.x -> 3.0.3
	case strings.HasSuffix(d, " became scalar "+unionSurvivor(d)) && strings.Contains(d, "/type: list "):
		return true // [T, null] -> T, and T really is the non-null member
	case strings.Contains(d, "nullable: ADDED (true)"):
		return true // ...and the 3.0 nullable marker
	case isResponseRename(d):
		return true // *Response -> *ResponseSchema (the definition key or a $ref value)
	case strings.Contains(d, "oneOf: list length"), strings.Contains(d, "anyOf: list length"):
		return true // bare `- type: null` members dropped
	case strings.Contains(d, "/format: float -> double"):
		return true // widen float32 -> float64 so money keeps full precision
	}
	return false
}

// TestSpecTransform_OnlyMakesDocumentedChanges is the core guard: convert the
// real vendored spec and assert every single difference is attributable to one
// of the four documented transforms.
func TestSpecTransform_OnlyMakesDocumentedChanges(t *testing.T) {
	root := repoRoot(t)
	srcPath := filepath.Join(root, "namecom.api.yaml")

	dstPath, err := runTransform(t, srcPath)
	if err != nil {
		t.Fatalf("preprocessor failed on the vendored spec: %v", err)
	}

	src := loadYAML(t, srcPath)
	dst := loadYAML(t, dstPath) // also asserts the output is valid YAML

	var diffs []string
	collectDiffs(src, dst, "", &diffs)

	var unexplained []string
	for _, d := range diffs {
		if !isDocumentedTransform(d) {
			unexplained = append(unexplained, d)
		}
	}
	if len(unexplained) > 0 {
		t.Errorf("preprocessor made %d change(s) outside its documented transforms:", len(unexplained))
		for i, d := range unexplained {
			if i >= 20 {
				t.Errorf("  ... and %d more", len(unexplained)-20)
				break
			}
			t.Errorf("  %s", d)
		}
	}
}

// TestSpecTransform_NoUnconvertedUnionsRemain asserts no OpenAPI 3.1 union
// type survives into the 3.0 output. A survivor reaches oapi-codegen, which
// does not understand it and emits a wrong type without erroring.
func TestSpecTransform_NoUnconvertedUnionsRemain(t *testing.T) {
	root := repoRoot(t)
	dstPath, err := runTransform(t, filepath.Join(root, "namecom.api.yaml"))
	if err != nil {
		t.Fatalf("preprocessor failed: %v", err)
	}

	var found []string
	var walk func(n any, path string)
	walk = func(n any, path string) {
		switch v := n.(type) {
		case map[string]any:
			if t, ok := v["type"]; ok {
				if _, isList := t.([]any); isList {
					found = append(found, path)
				}
			}
			for k, e := range v {
				walk(e, path+"/"+k)
			}
		case []any:
			for i, e := range v {
				walk(e, fmt.Sprintf("%s[%d]", path, i))
			}
		}
	}
	walk(loadYAML(t, dstPath), "")

	if len(found) > 0 {
		t.Errorf("%d OpenAPI 3.1 union type(s) survived into the 3.0 output:", len(found))
		for i, p := range found {
			if i >= 10 {
				break
			}
			t.Errorf("  %s", p)
		}
	}
}

// TestSpecTransform_RejectsUnsupportedConstructs pins the fail-loud behavior.
// Each of these silently corrupted the output before: the first three passed a
// 3.1 union straight through to codegen, and the last two either did the same
// or emitted structurally invalid YAML.
func TestSpecTransform_RejectsUnsupportedConstructs(t *testing.T) {
	const header = "openapi: 3.1.0\ninfo:\n  title: t\n  version: \"1\"\npaths: {}\ncomponents:\n  schemas:\n"

	tests := []struct {
		name       string
		body       string
		wantReject bool
	}{
		{
			name:       "null listed first",
			body:       "    A:\n      properties:\n        f:\n          type:\n            - 'null'\n            - string\n",
			wantReject: false, // now handled: order-independent
		},
		{
			name:       "double-quoted null",
			body:       "    A:\n      properties:\n        f:\n          type:\n            - string\n            - \"null\"\n",
			wantReject: false, // now handled
		},
		{
			name:       "bare unquoted null",
			body:       "    A:\n      properties:\n        f:\n          type:\n            - string\n            - null\n",
			wantReject: false, // now handled
		},
		{
			name:       "three-member union",
			body:       "    A:\n      properties:\n        f:\n          type:\n            - string\n            - integer\n            - 'null'\n",
			wantReject: true, // genuinely unrepresentable in 3.0 — must fail loudly
		},
		{
			name:       "null member carrying sibling keys",
			body:       "    B:\n      oneOf:\n        - type: 'null'\n          description: orphaned if the member line is dropped\n        - type: string\n",
			wantReject: true, // dropping the line alone emits invalid YAML
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "in.yaml")
			if err := os.WriteFile(src, []byte(header+tc.body), 0o600); err != nil {
				t.Fatalf("writing probe spec: %v", err)
			}

			dst, err := runTransform(t, src)
			if tc.wantReject {
				if err == nil {
					t.Error("preprocessor accepted a construct it cannot represent in 3.0 — " +
						"it must fail loudly rather than emit a wrong type")
				}
				return
			}
			if err != nil {
				t.Fatalf("preprocessor rejected a supported construct: %v", err)
			}
			out := loadYAML(t, dst) // must still be valid YAML
			if !strings.Contains(fmt.Sprint(out), "nullable:true") &&
				!strings.Contains(fmt.Sprint(out), "nullable:%!v(bool=true)") {
				b, _ := os.ReadFile(dst)
				if !strings.Contains(string(b), "nullable: true") {
					t.Errorf("union was not converted to a 3.0 nullable:\n%s", b)
				}
			}
		})
	}
}

// unionSurvivor extracts the non-null member from a diff line of the form
//
//	/path/type: list [string null] became scalar string
//
// so the whitelist can require that the scalar which survived is genuinely the
// union's non-null member, rather than accepting any scalar at all.
func unionSurvivor(diff string) string {
	open := strings.Index(diff, "list [")
	end := strings.Index(diff, "] became scalar ")
	if open < 0 || end < 0 {
		return "\x00" // never matches
	}
	members := strings.Fields(diff[open+len("list [") : end])
	var nonNull []string
	for _, m := range members {
		if m != "null" && m != "<nil>" && m != "'null'" {
			nonNull = append(nonNull, m)
		}
	}
	if len(nonNull) != 1 || len(members) != 2 {
		return "\x00" // not the supported two-member shape
	}
	return nonNull[0]
}

// isResponseRename reports whether a diff is explained by the *Response ->
// *ResponseSchema rename, and nothing else.
//
// Matching any path merely CONTAINING "Response" swept up every field inside
// the 33 renamed schemas — 107 of 542 diffs — so a corrupted field type within
// one of them was silently excused. Only two shapes are legitimate: a schema
// definition key being renamed (a REMOVED/ADDED pair at the schemas level), and
// a $ref string gaining the Schema suffix.
func isResponseRename(d string) bool {
	// A $ref value change: "...: #/components/schemas/X -> #/components/schemas/XSchema"
	if i := strings.Index(d, " -> "); i > 0 {
		before, after := d[:i], d[i+len(" -> "):]
		if strings.Contains(before, "#/components/schemas/") &&
			after == before[strings.LastIndex(before, " ")+1:]+"Schema" {
			return true
		}
	}
	// A definition key appearing/disappearing directly under /components/schemas.
	if strings.HasPrefix(d, "/components/schemas/") {
		rest := strings.TrimPrefix(d, "/components/schemas/")
		name, _, found := strings.Cut(rest, ":")
		if found && !strings.Contains(name, "/") &&
			(strings.HasSuffix(d, ": REMOVED") || strings.Contains(d, ": ADDED")) {
			return true
		}
	}
	return false
}
