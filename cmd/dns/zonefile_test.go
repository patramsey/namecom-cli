package dns

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
)

func p64(n int64) *int64 { return &n }

// rec is an inputRecord without its Source, for comparing parse results.
type rec struct {
	Type, Host, Answer string
	TTL                int64
	Priority           *int64
}

func stripSource(in []inputRecord) []rec {
	out := make([]rec, len(in))
	for i, r := range in {
		out[i] = rec{r.Type, r.Host, r.Answer, r.TTL, r.Priority}
	}
	return out
}

// TestParseZone covers the subset `dns export --zone` writes and the forms
// common in hand-written zone files.
func TestParseZone(t *testing.T) {
	cases := []struct {
		name string
		zone string
		want []rec
	}{
		{
			name: "dns export --zone output",
			zone: "example.com.\t300\tIN\tA\t1.2.3.4\n" +
				"www.example.com.\t300\tIN\tCNAME\texample.com.\n" +
				"example.com.\t300\tIN\tMX\t10 mail.example.net.\n" +
				"_sip._tcp.example.com.\t300\tIN\tSRV\t5 10 5060 sip.example.com.\n" +
				"example.com.\t300\tIN\tTXT\t\"v=spf1 include:_spf.example.com ~all\"\n" +
				anameCommentPrefix + "example.com.\t300\tIN\tANAME\tlb.example.net.\n",
			want: []rec{
				{"A", "@", "1.2.3.4", 300, nil},
				{"CNAME", "www", "example.com", 300, nil},
				{"MX", "@", "mail.example.net", 300, p64(10)},
				{"SRV", "_sip._tcp", "10 5060 sip.example.com", 300, p64(5)},
				{"TXT", "@", "v=spf1 include:_spf.example.com ~all", 300, nil},
				{"ANAME", "@", "lb.example.net", 300, nil},
			},
		},
		{
			name: "origin, ttl, @, relative names and blank owners",
			zone: `$ORIGIN example.com.
$TTL 1h
@       IN  A     192.0.2.1
        IN  AAAA  2001:db8::1     ; same owner as the line above
www     600 CNAME @
mail    IN 900 A  192.0.2.2
@          MX    20 mail
$ORIGIN sub.example.com.
api        A     192.0.2.3
`,
			want: []rec{
				{"A", "@", "192.0.2.1", 3600, nil},
				{"AAAA", "@", "2001:db8::1", 3600, nil},
				{"CNAME", "www", "example.com", 600, nil},
				{"A", "mail", "192.0.2.2", 900, nil},
				{"MX", "@", "mail.example.com", 3600, p64(20)},
				{"A", "api.sub", "192.0.2.3", 3600, nil},
			},
		},
		{
			name: "no $TTL: the last explicit TTL repeats, the first defaults",
			zone: "a A 192.0.2.1\nb 900 A 192.0.2.2\nc A 192.0.2.3\n",
			want: []rec{
				{"A", "a", "192.0.2.1", defaultTTL, nil},
				{"A", "b", "192.0.2.2", 900, nil},
				{"A", "c", "192.0.2.3", 900, nil},
			},
		},
		{
			name: "quoted TXT chunks join, escapes decode, semicolons inside quotes stay",
			zone: `dkim._domainkey TXT ( "v=DKIM1; k=rsa; "
                        "p=MIGf" )  ; split over two lines
quote TXT "say \"hi\"\059 bye" unquoted
`,
			want: []rec{
				{"TXT", "dkim._domainkey", "v=DKIM1; k=rsa; p=MIGf", defaultTTL, nil},
				{"TXT", "quote", `say "hi"; byeunquoted`, defaultTTL, nil},
			},
		},
		{
			name: "SOA in parentheses is skipped; CRLF line endings; comments",
			zone: "; a hand-written zone\r\n" +
				"$TTL 86400\r\n" +
				"@ IN SOA ns1.example.com. admin.example.com. (\r\n" +
				"    2024010101 ; serial\r\n" +
				"    3600 900 604800 300 )\r\n" +
				"@ IN NS ns1.name.com.\r\n" +
				"* IN A 192.0.2.9\r\n",
			want: []rec{
				{"NS", "@", "ns1.name.com", 86400, nil},
				{"A", "*", "192.0.2.9", 86400, nil},
			},
		},
		{
			name: "absolute names, mixed case, null MX, CAA",
			zone: "WWW.Example.COM. 300 IN A 192.0.2.1\n" +
				"example.com. 300 IN MX 0 .\n" +
				"example.com. 300 IN CAA 0 issue \"letsencrypt.org\"\n",
			want: []rec{
				{"A", "www", "192.0.2.1", 300, nil},
				{"MX", "@", ".", 300, p64(0)},
				{"CAA", "@", `0 issue "letsencrypt.org"`, 300, nil},
			},
		},
		{
			name: "BIND TTL units",
			zone: "a 1w A 192.0.2.1\nb 1d2h A 192.0.2.2\nc 90M A 192.0.2.3\n",
			want: []rec{
				{"A", "a", "192.0.2.1", 604800, nil},
				{"A", "b", "192.0.2.2", 93600, nil},
				{"A", "c", "192.0.2.3", 5400, nil},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseZone(tc.zone, "example.com")
			if err != nil {
				t.Fatalf("parseZone: %v", err)
			}
			if g := stripSource(got); !reflect.DeepEqual(g, tc.want) {
				t.Errorf("parsed:\n  %+v\nwant:\n  %+v", g, tc.want)
			}
		})
	}
}

// TestParseZone_Errors pins that nothing in a zone file is silently dropped:
// what the parser cannot represent is an error naming the line.
func TestParseZone_Errors(t *testing.T) {
	cases := map[string]struct{ zone, want string }{
		"outside the zone":     {"www.example.net. 300 IN A 192.0.2.1\n", "line 1: owner www.example.net. is outside the zone example.com."},
		"origin outside":       {"$ORIGIN example.net.\n", "line 1: $ORIGIN example.net. is outside the zone"},
		"include":              {"$INCLUDE other.zone\n", "line 1: $INCLUDE is not supported"},
		"unsupported type":     {"\n\nhost PTR x.example.com.\n", "line 3: PTR host: record type PTR is not one name.com hosts"},
		"other class":          {"host CH A 192.0.2.1\n", "class CH is not supported"},
		"unterminated quote":   {"host TXT \"abc\n", "line 1: unterminated quoted string"},
		"unclosed paren":       {"host TXT ( \"abc\"\n", "unclosed '('"},
		"stray paren":          {"host A 192.0.2.1 )\n", "line 1: unbalanced ')'"},
		"MX without priority":  {"@ MX mail.example.com.\n", "line 1: MX @: expected preference and exchange"},
		"blank owner first":    {"  A 192.0.2.1\n", "line 1: the first record must name its owner"},
		"bad priority":         {"@ MX 70000 mail\n", `priority "70000" must be an integer 0-65535`},
		"missing type":         {"host 300 IN\n", "line 1: missing record type"},
		"incomplete escape":    {"host TXT \"a\\05\"\n", `incomplete \DDD escape`},
		"quoted address field": {"host A \"192.0.2.1\"\n", "got a quoted string"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseZone(tc.zone, "example.com")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("parseZone error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestParseZone_RoundTripsExport pins that what `dns export --zone` writes
// reads back as the records it was written from — the ANAME kept in its
// comment included — so export, edit, sync is lossless.
func TestParseZone_RoundTripsExport(t *testing.T) {
	body := `{"records":[
	  {"id":1,"host":"","fqdn":"example.com.","type":"A","answer":"1.2.3.4","ttl":300},
	  {"id":2,"host":"www","fqdn":"www.example.com.","type":"CNAME","answer":"example.com","ttl":600},
	  {"id":3,"host":"","fqdn":"example.com.","type":"MX","answer":"mail.example.com","ttl":300,"priority":10},
	  {"id":4,"host":"_sip._tcp","fqdn":"_sip._tcp.example.com.","type":"SRV","answer":"10 5060 sip.example.com","ttl":300,"priority":5},
	  {"id":5,"host":"","fqdn":"example.com.","type":"TXT","answer":"` + strings.Repeat("k", 300) + `","ttl":300},
	  {"id":6,"host":"","fqdn":"example.com.","type":"ANAME","answer":"lb.example.net","ttl":300}
	]}`
	cmd, buf := cmdForExport(t, recordsServer(t, body), output.FormatTable)
	exportZone = true
	t.Cleanup(func() { exportZone = false })
	if err := runExport(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runExport: %v", err)
	}
	got, err := parseZone(buf.String(), "example.com")
	if err != nil {
		t.Fatalf("parsing exported zone: %v\n%s", err, buf.String())
	}
	want := []rec{
		{"A", "@", "1.2.3.4", 300, nil},
		{"CNAME", "www", "example.com", 600, nil},
		{"MX", "@", "mail.example.com", 300, p64(10)},
		{"SRV", "_sip._tcp", "10 5060 sip.example.com", 300, p64(5)},
		{"TXT", "@", strings.Repeat("k", 300), 300, nil},
		{"ANAME", "@", "lb.example.net", 300, nil},
	}
	if g := stripSource(got); !reflect.DeepEqual(g, want) {
		t.Errorf("round trip:\n  %+v\nwant:\n  %+v", g, want)
	}
}

// TestReadRecordsFile_DetectsFormat pins the format detection: a .json
// extension or JSON content is JSON, anything else a zone file, and JSON
// wrapped in a list envelope is unwrapped.
func TestReadRecordsFile_DetectsFormat(t *testing.T) {
	cases := []struct {
		file, content, format string
		want                  []rec
	}{
		{"records.json", `[{"type":"a","host":"","answer":"1.2.3.4"}]`, formatJSON,
			[]rec{{"A", "@", "1.2.3.4", defaultTTL, nil}}},
		{"export.txt", "\n  [{\"type\":\"A\",\"host\":\"www\",\"answer\":\"1.2.3.4\",\"ttl\":600}]", formatJSON,
			[]rec{{"A", "www", "1.2.3.4", 600, nil}}},
		{"list.out", `{"data":[{"type":"A","host":"www","answer":"1.2.3.4","ttl":600}],"nextPage":null}`, formatJSON,
			[]rec{{"A", "www", "1.2.3.4", 600, nil}}},
		{"example.com.zone", "www 600 IN A 1.2.3.4\n", formatZone,
			[]rec{{"A", "www", "1.2.3.4", 600, nil}}},
		{"zone.txt", "; comment first\n@ A 1.2.3.4\n", formatZone,
			[]rec{{"A", "@", "1.2.3.4", defaultTTL, nil}}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.file)
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			got, format, err := readRecordsFile(path, "example.com")
			if err != nil {
				t.Fatalf("readRecordsFile: %v", err)
			}
			if format != tc.format {
				t.Errorf("format = %s, want %s", format, tc.format)
			}
			if g := stripSource(got); !reflect.DeepEqual(g, tc.want) {
				t.Errorf("records:\n  %+v\nwant:\n  %+v", g, tc.want)
			}
		})
	}

	t.Run("a bad zone file is a usage error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bad.zone")
		if err := os.WriteFile(path, []byte("www IN PTR x.\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := readRecordsFile(path, "example.com")
		var ue *cmdutil.UsageError
		if !errors.As(err, &ue) {
			t.Errorf("expected a usage error, got %v", err)
		}
	})
}

// FuzzParseZone pins that no input makes the parser panic, and that every
// record it returns has a type and a host relative to the zone.
func FuzzParseZone(f *testing.F) {
	for _, seed := range []string{
		"$ORIGIN example.com.\n$TTL 1h\n@ IN A 192.0.2.1\n  IN AAAA ::1\n",
		"www ( 300\n IN CNAME @ )\n",
		"t TXT \"a\\\"b\" \"\\065\" ; c\n",
		"@ IN SOA a. b. ( 1 2 3 4 5 )\n",
		anameCommentPrefix + "example.com.\t300\tIN\tANAME\tx.example.net.\n",
		"x MX 10 .\r\n_s._tcp SRV 1 2 3 t\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, zone string) {
		recs, err := parseZone(zone, "example.com")
		if err != nil {
			return
		}
		for _, r := range recs {
			if r.Type == "" || r.Host == "" || strings.HasSuffix(r.Host, ".") {
				t.Errorf("record %+v from %q has no type, or a host that is not relative", r, zone)
			}
		}
	})
}

func TestParseTTL(t *testing.T) {
	for in, want := range map[string]int64{"0": 0, "300": 300, "1h": 3600, "1H30M": 5400, "2d": 172800, "1w1d": 691200, "1h30": 3630} {
		if got, ok := parseTTL(in); !ok || got != want {
			t.Errorf("parseTTL(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "A", "IN", "h", "1x", "-5", "4294967296", "99999w"} {
		if _, ok := parseTTL(in); ok {
			t.Errorf("parseTTL(%q) accepted a non-TTL", in)
		}
	}
}
