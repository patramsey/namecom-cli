package cmdutil

import (
	"encoding/json"
	"fmt"
	"os"

	coreapigo "github.com/namedotcom/core-api-go"
)

// ReadContactsFile reads a --contacts-file: a ContactsRequest as JSON, the
// format `domain register` and `transfer create` both take. The errors are
// unclassified; a caller that reads the file before any request decides
// whether a bad one is a usage error.
func ReadContactsFile(path string) (*coreapigo.ContactsRequest, error) {
	f, err := os.ReadFile(path) //nolint:gosec // G304: --contacts-file names the file to read; that is the flag's purpose
	if err != nil {
		return nil, fmt.Errorf("reading contacts file: %w", err)
	}
	var contacts coreapigo.ContactsRequest
	if err := json.Unmarshal(f, &contacts); err != nil {
		return nil, fmt.Errorf("parsing contacts file: %w", err)
	}
	return &contacts, nil
}
