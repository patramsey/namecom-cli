package domain

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/patramsey/namecom-cli/internal/api/gen"
)

func TestDiagnose_UpdateFlow(t *testing.T) {
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gen.DomainResponsePayload{DomainName: "example.com"})
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForUpdate(t, srv)
	err := runUpdate(cmd, []string{"EXAMPLE.COM"})
	fmt.Printf("err: %v\n", err)
	fmt.Printf("requests: %v\n", requests)
}
