package cmdutil

import (
	"encoding/json"
	"fmt"
	"os"

	coreapigo "github.com/namedotcom/core-api-go"
)

// ReadContactsFile reads a contacts file: a ContactsRequest as JSON, the
// format `domain register --contacts-file`, `domain contacts set --from-file`
// and `transfer create --contacts-file` all take. A missing, unreadable or
// invalid file is a usage error (exit 2), so callers read it before any
// request or prompt.
func ReadContactsFile(path string) (*coreapigo.ContactsRequest, error) {
	f, err := os.ReadFile(path) //nolint:gosec // G304: --contacts-file names the file to read; that is the flag's purpose
	if err != nil {
		return nil, NewUsageError(fmt.Errorf("reading contacts file: %w", err))
	}
	var contacts coreapigo.ContactsRequest
	if err := json.Unmarshal(f, &contacts); err != nil {
		return nil, NewUsageError(fmt.Errorf("parsing contacts file: %w", err))
	}
	return &contacts, nil
}
