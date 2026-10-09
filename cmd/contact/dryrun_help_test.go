package contact

import (
	"strings"
	"testing"
)

// TestDryRunHelp_SaysTargetUnchecked pins #326: resend and verify dry runs
// preview without checking the verification record, since the API has no
// read for one. The help says so rather than leaving a passing dry run to
// promise a write that may fail.
func TestDryRunHelp_SaysTargetUnchecked(t *testing.T) {
	for _, c := range []string{resendCmd.Long, verifyCmd.Long} {
		if !strings.Contains(c, "--dry-run does not check that the verification record exists") {
			t.Errorf("help does not say what --dry-run leaves unchecked:\n%s", c)
		}
	}
}
