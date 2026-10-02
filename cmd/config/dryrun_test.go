package config

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/internal/output"
)

// TestUse_DryRunLeavesConfig guards #166: `config use X --dry-run` changed the
// default profile. Under --dry-run it describes the change and writes nothing.
func TestUse_DryRunLeavesConfig(t *testing.T) {
	cmd, buf := configCmd(t, output.FormatJSON)
	cmd.PersistentFlags().Bool("dry-run", true, "")
	path := os.Getenv("NAMECOM_CONFIG")
	before, err := os.ReadFile(path) //nolint:gosec // test temp file
	if err != nil {
		t.Fatal(err)
	}

	if err := runUse(cmd, []string{"helper"}); err != nil {
		t.Fatalf("runUse --dry-run: %v", err)
	}

	after, err := os.ReadFile(path) //nolint:gosec // test temp file
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("--dry-run rewrote the config:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("dry-run output is not one JSON document: %v\n%s", err, buf.String())
	}
	if got["dry_run"] != true || got["action"] != "set_default" || got["profile"] != "helper" ||
		got["default"] != "helper" || got["config"] != path {
		t.Errorf("dry-run should describe making helper the default in %s, got %v", path, got)
	}
}

// TestUse_DryRunTable: the human preview names the change and the file, and
// does not claim it was made.
func TestUse_DryRunTable(t *testing.T) {
	cmd, buf := configCmd(t, output.FormatTable)
	cmd.PersistentFlags().Bool("dry-run", true, "")

	if err := runUse(cmd, []string{"helper"}); err != nil {
		t.Fatalf("runUse --dry-run: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "dry-run") || !strings.Contains(got, `"helper"`) ||
		!strings.Contains(got, os.Getenv("NAMECOM_CONFIG")) {
		t.Errorf("table dry-run should name the change and the file, got:\n%s", got)
	}
	if strings.Contains(got, "Default profile set") {
		t.Errorf("dry-run reported the change as made:\n%s", got)
	}
}
