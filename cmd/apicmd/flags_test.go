package apicmd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// request is what a recordServer saw of one request.
type request struct {
	method, uri, body, contentType string
}

// recordServer records every request it serves and answers each with reply.
func recordServer(t *testing.T, reply string) (*httptest.Server, *[]request) {
	t.Helper()
	var got []request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, request{r.Method, r.URL.RequestURI(), string(b), r.Header.Get("Content-Type")})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

// isUsage reports whether err is a usage error, which exits 2.
func isUsage(err error) bool {
	var ue *cmdutil.UsageError
	return errors.As(err, &ue)
}

// jsonEqual fails the test unless got and want are the same JSON value.
func jsonEqual(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("bad want: %v", err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

// TestAPI_MethodInferred pins #245's method inference: the method may be
// left out, and is GET, or POST when the request has a body. The explicit
// form keeps working (the rest of this package's tests use it).
func TestAPI_MethodInferred(t *testing.T) {
	for _, tc := range []struct {
		name       string
		set        func()
		wantMethod string
	}{
		{"no body", func() {}, "GET"},
		{"--data", func() { apiBody = `{"a":1}` }, "POST"},
		{"-f", func() { apiFields = []string{"a=1"} }, "POST"},
		{"-F", func() { apiTyped = []string{"a=1"} }, "POST"},
		{"--input", func() {
			p := filepath.Join(t.TempDir(), "b.json")
			_ = os.WriteFile(p, []byte(`{}`), 0o600)
			apiInput = p
		}, "POST"},
		{"--paginate with -f", func() { apiPaginate = true; apiFields = []string{"perPage=2"} }, "GET"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := recordServer(t, `{}`)
			cmd, _ := apiCmd(t, srv)
			tc.set()
			if err := runAPI(cmd, []string{"/core/v1/domains"}); err != nil {
				t.Fatalf("runAPI: %v", err)
			}
			if len(*got) != 1 || (*got)[0].method != tc.wantMethod {
				t.Errorf("sent %+v, want one %s", *got, tc.wantMethod)
			}
		})
	}

	t.Run("a method alone is a usage error", func(t *testing.T) {
		cmd, _ := apiCmd(t, refuseAll(t))
		cmd.Use = "api [METHOD] <path>"
		if err := runAPI(cmd, []string{"get"}); !isUsage(err) {
			t.Errorf("err = %v, want a usage error", err)
		}
	})
}

// TestAPI_InferredPostIsStillAWrite: a POST the method was inferred for goes
// through RunWrite, so --dry-run previews it rather than sending it.
func TestAPI_InferredPostIsStillAWrite(t *testing.T) {
	cmd, buf := apiCmd(t, refuseAll(t))
	dryRun(cmd, true)
	apiFields = []string{"host=www"}
	if err := runAPI(cmd, []string{"/core/v1/domains/example.com/records"}); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	doc := parseDryRun(t, buf)
	if doc.Method != "POST" {
		t.Errorf("previewed %s, want POST", doc.Method)
	}
	jsonEqual(t, string(doc.Body), `{"host":"www"}`)
}

// TestAPI_FieldsBuildBody pins -f and -F: -f values are strings, -F keeps
// true/false/null and numbers as JSON, @file is the file's contents, and
// keys nest with [key] and append with [].
func TestAPI_FieldsBuildBody(t *testing.T) {
	file := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(file, []byte("from a file"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, got := recordServer(t, `{}`)
	cmd, _ := apiCmd(t, srv)
	apiFields = []string{"host=@", "ttl=300", "contact[name]=Ada", "ns[]=ns1", "ns[]=ns2"}
	apiTyped = []string{"n=300", "f=-1.5", "yes=true", "no=false", "nil=null", "word=abc",
		"note=@" + file, "contact[age]=36", "s=[1]"}
	if err := runAPI(cmd, []string{"POST", "/core/v1/x"}); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	r := (*got)[0]
	if r.contentType != "application/json" {
		t.Errorf("Content-Type = %q", r.contentType)
	}
	jsonEqual(t, r.body, `{"host":"@","ttl":"300","contact":{"name":"Ada","age":36},
		"ns":["ns1","ns2"],"n":300,"f":-1.5,"yes":true,"no":false,"nil":null,
		"word":"abc","note":"from a file","s":"[1]"}`)
}

// TestAPI_FieldFromStdin: -F key=@- is stdin's contents.
func TestAPI_FieldFromStdin(t *testing.T) {
	stdinWith(t, "piped")
	srv, got := recordServer(t, `{}`)
	cmd, _ := apiCmd(t, srv)
	apiTyped = []string{"note=@-"}
	if err := runAPI(cmd, []string{"/core/v1/x"}); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	jsonEqual(t, (*got)[0].body, `{"note":"piped"}`)
}

// TestAPI_FieldsAreQueryForReads: on a GET, fields are query parameters,
// after any the path already has, and there is no body. The GET is named:
// with the method left out, fields make it a POST.
func TestAPI_FieldsAreQueryForReads(t *testing.T) {
	srv, got := recordServer(t, `{}`)
	cmd, _ := apiCmd(t, srv)
	apiFields = []string{"perPage=2"}
	apiTyped = []string{"x=true"}
	if err := runAPI(cmd, []string{"GET", "/core/v1/domains?sort=asc"}); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	r := (*got)[0]
	if r.method != "GET" || r.uri != "/core/v1/domains?sort=asc&perPage=2&x=true" || r.body != "" {
		t.Errorf("sent %+v", r)
	}
}

// TestAPI_BadFieldsAreUsageErrors: a malformed field, or two that cannot
// both be set, is caught before anything is sent.
func TestAPI_BadFieldsAreUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		raw, typed []string
	}{
		{"no =", []string{"host"}, nil},
		{"no key", []string{"=x"}, nil},
		{"repeated", []string{"a=1", "a=2"}, nil},
		{"value then object", []string{"a=1", "a[b]=2"}, nil},
		{"object then list", []string{"a[b]=1", "a[]=2"}, nil},
		{"[] not last", []string{"a[][b]=1"}, nil},
		{"unclosed", []string{"a[b=1"}, nil},
		{"typed no =", nil, []string{"x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, _ := apiCmd(t, refuseAll(t))
			apiFields, apiTyped = tc.raw, tc.typed
			if err := runAPI(cmd, []string{"POST", "/core/v1/x"}); !isUsage(err) {
				t.Errorf("err = %v, want a usage error", err)
			}
		})
	}
}

// TestAPI_Input pins --input: the body is the file, or stdin for '-'.
func TestAPI_Input(t *testing.T) {
	const payload = `{"host":"www"}`
	t.Run("file", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "record.json")
		if err := os.WriteFile(p, []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
		srv, got := recordServer(t, `{}`)
		cmd, _ := apiCmd(t, srv)
		apiInput = p
		if err := runAPI(cmd, []string{"PUT", "/core/v1/x"}); err != nil {
			t.Fatalf("runAPI: %v", err)
		}
		if r := (*got)[0]; r.body != payload || r.contentType != "application/json" {
			t.Errorf("sent %+v", r)
		}
	})
	t.Run("stdin", func(t *testing.T) {
		stdinWith(t, payload)
		srv, got := recordServer(t, `{}`)
		cmd, _ := apiCmd(t, srv)
		apiInput = "-"
		if err := runAPI(cmd, []string{"/core/v1/x"}); err != nil {
			t.Fatalf("runAPI: %v", err)
		}
		if r := (*got)[0]; r.method != "POST" || r.body != payload {
			t.Errorf("sent %+v", r)
		}
	})
	t.Run("previewed", func(t *testing.T) {
		stdinWith(t, payload)
		cmd, buf := apiCmd(t, refuseAll(t))
		dryRun(cmd, true)
		apiInput = "-"
		if err := runAPI(cmd, []string{"POST", "/core/v1/x"}); err != nil {
			t.Fatalf("runAPI: %v", err)
		}
		jsonEqual(t, string(parseDryRun(t, buf).Body), payload)
	})
	t.Run("missing file", func(t *testing.T) {
		cmd, _ := apiCmd(t, refuseAll(t))
		apiInput = filepath.Join(t.TempDir(), "nope.json")
		err := runAPI(cmd, []string{"POST", "/core/v1/x"})
		if err == nil || isUsage(err) {
			t.Errorf("err = %v, want a plain error", err)
		}
	})
}

// TestAPI_ConflictingFlags: flags that would each give the body, --paginate
// on anything but GET, and --include with a JSON filter are usage errors,
// and nothing is sent.
func TestAPI_ConflictingFlags(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		set    func(cmd *cobra.Command)
		reason string
	}{
		{"--input and -f", []string{"/x"}, func(*cobra.Command) { apiInput = "-"; apiFields = []string{"a=b"} }, "--input"},
		{"--input and --data", []string{"/x"}, func(*cobra.Command) { apiInput = "-"; apiBody = "{}" }, "--input"},
		{"--data and -F", []string{"/x"}, func(*cobra.Command) { apiBody = "{}"; apiTyped = []string{"a=1"} }, "--data"},
		{"--paginate POST", []string{"POST", "/x"}, func(*cobra.Command) { apiPaginate = true }, "--paginate"},
		{"--paginate HEAD", []string{"HEAD", "/x"}, func(*cobra.Command) { apiPaginate = true }, "--paginate"},
		{"--include --jq", []string{"/x"}, func(cmd *cobra.Command) { apiInclude = true; setGlobal(t, cmd, "jq", ".") }, "--jq"},
		{"--include --fields", []string{"/x"}, func(cmd *cobra.Command) { apiInclude = true; setGlobal(t, cmd, "fields", "a") }, "--fields"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, _ := apiCmd(t, refuseAll(t))
			tc.set(cmd)
			err := runAPI(cmd, tc.args)
			if !isUsage(err) || !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("err = %v, want a usage error naming %s", err, tc.reason)
			}
		})
	}
}

// setGlobal hangs cmd under a root with the global flag name set to val.
func setGlobal(t *testing.T, cmd *cobra.Command, name, val string) {
	t.Helper()
	root := &cobra.Command{Use: "namecom"}
	root.PersistentFlags().String(name, "", "")
	root.AddCommand(cmd)
	if err := root.PersistentFlags().Set(name, val); err != nil {
		t.Fatal(err)
	}
}

// pagedServer serves a list of 5 domains, 2 per page, the way the API pages
// its lists: nextPage and lastPage until the last page, which has neither.
func pagedServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var uris []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uris = append(uris, r.URL.RequestURI())
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}
		var names []string
		for i := (page-1)*2 + 1; i <= min(page*2, 5); i++ {
			names = append(names, `{"domainName":"d`+strconv.Itoa(i)+`.com"}`)
		}
		doc := `{"domains":[` + strings.Join(names, ",") + `],"totalCount":5,` +
			`"from":` + strconv.Itoa((page-1)*2+1) + `,"to":` + strconv.Itoa(min(page*2, 5))
		if page < 3 {
			doc += `,"nextPage":` + strconv.Itoa(page+1) + `,"lastPage":3`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(doc + "}"))
	}))
	t.Cleanup(srv.Close)
	return srv, &uris
}

const mergedDomains = `{"domains":[{"domainName":"d1.com"},{"domainName":"d2.com"},` +
	`{"domainName":"d3.com"},{"domainName":"d4.com"},{"domainName":"d5.com"}],` +
	`"totalCount":5,"from":1,"to":5}` + "\n"

// TestAPI_Paginate pins --paginate: every page is fetched, each with the
// path's own query kept, and printed as one document whose lists are every
// page's items, without nextPage or lastPage.
func TestAPI_Paginate(t *testing.T) {
	srv, uris := pagedServer(t)
	cmd, buf := apiCmd(t, srv)
	apiPaginate = true
	apiFields = []string{"perPage=2"}
	if err := runAPI(cmd, []string{"/core/v1/domains?sort=asc"}); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	if got := buf.String(); got != mergedDomains {
		t.Errorf("printed %s\nwant %s", got, mergedDomains)
	}
	want := []string{
		"/core/v1/domains?sort=asc&perPage=2",
		"/core/v1/domains?page=2&perPage=2&sort=asc",
		"/core/v1/domains?page=3&perPage=2&sort=asc",
	}
	if !reflect.DeepEqual(*uris, want) {
		t.Errorf("requested %q\nwant %q", *uris, want)
	}
}

// TestAPI_PaginateWithJQ: the global --jq filters the merged document, not
// each page.
func TestAPI_PaginateWithJQ(t *testing.T) {
	srv, _ := pagedServer(t)
	cmd, buf := apiCmd(t, srv)
	code, err := output.CompileJQ(".domains | length")
	if err != nil {
		t.Fatal(err)
	}
	out := cmdutil.Out(cmd)
	out.BeginFilter(&output.Filter{JQ: code})
	apiPaginate = true
	if err := runAPI(cmd, []string{"/core/v1/domains"}); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	if err := out.EndFilter(false); err != nil {
		t.Fatalf("EndFilter: %v", err)
	}
	if got := buf.String(); got != "5\n" {
		t.Errorf("--jq printed %q, want 5", got)
	}
}

// TestAPI_PaginateStopsOnError: a page that fails is the command's error,
// and nothing is printed — half a list would read as all of it.
func TestAPI_PaginateStopsOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		_, _ = w.Write([]byte(`{"domains":[{"domainName":"a.com"}],"nextPage":2}`))
	}))
	t.Cleanup(srv.Close)
	cmd, buf := apiCmd(t, srv)
	apiPaginate = true
	if err := runAPI(cmd, []string{"/core/v1/domains"}); err == nil {
		t.Fatal("want the second page's error")
	}
	if buf.Len() != 0 {
		t.Errorf("printed %q after a failed page", buf.String())
	}
}

// TestAPI_PaginateEdgeCases: a reply that is not an object prints as it
// came, and a nextPage that was already fetched stops instead of looping.
func TestAPI_PaginateEdgeCases(t *testing.T) {
	t.Run("not an object", func(t *testing.T) {
		srv, _ := recordServer(t, `[1,2]`)
		cmd, buf := apiCmd(t, srv)
		apiPaginate = true
		if err := runAPI(cmd, []string{"/core/v1/x"}); err != nil {
			t.Fatalf("runAPI: %v", err)
		}
		if buf.String() != "[1,2]\n" {
			t.Errorf("printed %q", buf.String())
		}
	})
	t.Run("loop", func(t *testing.T) {
		srv, got := recordServer(t, `{"items":[1],"nextPage":1}`)
		cmd, _ := apiCmd(t, srv)
		apiPaginate = true
		err := runAPI(cmd, []string{"/core/v1/x"})
		if err == nil || !strings.Contains(err.Error(), "page 1") {
			t.Errorf("err = %v, want it to name the repeated page", err)
		}
		if len(*got) != 1 {
			t.Errorf("sent %d requests, want 1", len(*got))
		}
	})
}

// TestAPI_Include pins -i: the status line and response headers come before
// the body on stdout, and the request's credential is never among them.
func TestAPI_Include(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "abc")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	cmd, buf := apiCmd(t, srv)
	apiInclude = true
	if err := runAPI(cmd, []string{"/core/v1/hello"}); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	got := buf.String()
	head, body, ok := strings.Cut(got, "\n\n")
	if !ok || body != `{"ok":true}`+"\n" {
		t.Fatalf("want headers, a blank line, then the body; got:\n%s", got)
	}
	if !strings.HasPrefix(head, "HTTP/1.1 200 OK\n") || !strings.Contains(head, "\nX-Request-Id: abc") {
		t.Errorf("head = %q", head)
	}
	// "alice:s3cret" base64-encoded, as apiCmd configures.
	if strings.Contains(got, "Authorization") || strings.Contains(got, "YWxpY2U6czNjcmV0") || strings.Contains(got, "s3cret") {
		t.Errorf("--include printed the credential:\n%s", got)
	}
}
