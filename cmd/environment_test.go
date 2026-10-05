package cmd

import (
	"bytes"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestEnvironmentTopic pins #237: NAMECOM_* variables were documented only in
// the README. Every one named in the non-test source must be in `namecom help
// environment`, so a new variable cannot be added without documenting it.
func TestEnvironmentTopic(t *testing.T) {
	envRE := regexp.MustCompile(`NAMECOM_[A-Z_]+`)
	found := map[string]string{}
	repo := os.DirFS("..")
	err := fs.WalkDir(repo, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); path != "." && (strings.HasPrefix(name, ".") || name == "testdata") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || d.Name() == "environment.go" {
			return nil
		}
		src, err := fs.ReadFile(repo, path)
		if err != nil {
			return err
		}
		for _, name := range envRE.FindAllString(string(src), -1) {
			found[name] = path
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) < 5 {
		t.Fatalf("found only %v; is the walk reading the source tree?", found)
	}
	for name, path := range found {
		if !strings.Contains(environmentCmd.Long, name+" ") {
			t.Errorf("%s (read in %s) is missing from 'namecom help environment'", name, path)
		}
	}

	// Reachable as a help topic, and listed in root help.
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	t.Cleanup(func() { rootCmd.SetOut(nil) })
	if err := executeRoot(t, "help", "environment"); err != nil {
		t.Fatalf("namecom help environment: %v", err)
	}
	if !strings.Contains(buf.String(), "NAMECOM_SANDBOX") || strings.Contains(buf.String(), "Usage:") {
		t.Errorf("namecom help environment should print the topic alone:\n%s", buf.String())
	}
	if !strings.Contains(section(renderHelp(t), "Help Topics:"), "environment") {
		t.Errorf("root help does not list the environment topic:\n%s", renderHelp(t))
	}
	if f := rootCmd.PersistentFlags().Lookup("sandbox"); !strings.Contains(f.Usage, "NAMECOM_SANDBOX") {
		t.Errorf("--sandbox does not name its variable: %q", f.Usage)
	}
}
