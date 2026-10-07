package cmd

import "github.com/spf13/cobra"

// formattingCmd is the `namecom help formatting` topic (#241): -o, --fields,
// --jq and -o tsv, and how they combine. Like `help environment`, it has no
// Run, which makes it a help topic.
var formattingCmd = &cobra.Command{
	Use:   "formatting",
	Short: "Output formats, --fields, --jq and TSV",
	Long: `Choosing what a command prints, and in what form.

Formats (-o, --output):

  table   The default in a terminal. Without one (a pipe or a file) the
          table has no borders: columns aligned with spaces.
  json    The default when output is not a terminal. The README's "JSON
          contract" describes the documents: a list is {"data": [...]}.
  yaml    The same keys as json.
  tsv     Tab-separated values for scripts. No colour, a date without
          "(in 3 months)", and an empty cell where a table shows "—".

  TSV has two shapes, and which one a command prints never depends on the
  data:

    A list      A header row (unless --no-header), then a row per item.
                The header prints for an empty list too. The columns are
                the table's, and the same whatever the items hold: dns list
                always has PRIORITY. Several objects (domain get a.com
                b.com, domain contacts get's roles) are a list, headed by
                the field names one object's rows use.
    One object  field<TAB>value rows, the same rows whatever it holds: a
                value it lacks is an empty cell, not a missing row. A
                command with a detail table (domain get) uses the table's
                field names; one without (status, version, auth status,
                config show, domain claims, open) uses the -o json keys,
                with bare values — where a value came from is a key of its
                own, such as profileSource — and a list in a value is a
                JSON array.

  A write prints its result's keys (success, changed, message) as one
  object, and a write to several targets a row each; a read never prints
  them. A dry run prints method, path and body as a list, and dns sync's
  plan a row per change: action, type, host, answer, TTL, priority. dns export writes a file, JSON or a zone file, so -o table,
  -o tsv and -q are usage errors there.

  In TSV a cell's backslash, tab, line feed and carriage return are written
  \\, \t, \n and \r, so every row is one line.

Picking fields (--fields a,b,c):

  Keeps only those keys, in that order, of each item of a list, or of the
  object a command prints. A list keeps its envelope ({"data": [...]} with
  nextPage and total). Works with every -o, in the shapes above: for a list
  the fields are the columns, and for one object the rows. A table keeps
  the list's footer on stderr, so a list cut short by --limit says so.
  Names are the JSON keys (see -o json); nested keys are not addressed, use
  --jq for those. An item without a field gets null for it. A field the
  output cannot have is a usage error that lists the fields there are,
  whether or not the list is empty.

  The values are the JSON's, not the table's: true rather than yes, a
  timestamp rather than a date, and an empty host, not @, for a zone apex.

    namecom domain list --fields domainName,expireDate -o tsv
    namecom domain get example.com --fields locked,autorenewEnabled
    namecom dns list example.com --fields id,type,host,answer -o table

Filtering with jq (--jq <expr>):

  Runs a jq expression over the JSON document -o json would print, with an
  embedded jq (gojq), so jq need not be installed. Each result prints on its
  own line: a string as itself, without quotes (as jq -r prints it), anything
  else as compact JSON. --jq means JSON; with -o table, yaml or tsv it is a
  usage error. With --fields, the fields are picked first.

    namecom domain list --all --jq '.data[].domainName'
    namecom domain list --jq '.data[] | select(.locked | not) | .domainName'
    namecom dns create example.com --type A --answer 192.0.2.1 --jq .id
    namecom domain renew example.com --dry-run --jq .quote.total

  gojq prints an object's keys in sorted order.

Errors and exit codes:

  A malformed expression, an unknown jq function, -q with --fields or --jq,
  and --jq with another -o are usage errors (exit 2) before anything is sent.
  An expression that fails on the output, and a field no item has, are usage
  errors too, with nothing printed — except after a write, where the change
  has been made: the output is printed unfiltered, with a warning.

  --fields and --jq act on stdout only. A command that fails prints its
  error on stderr, in the error envelope, with its own exit code.

-q, --quiet prints one ID or name per line whatever -o says, tsv included;
with --fields or --jq it is a usage error, since both choose what to print.`,
}

func init() {
	rootCmd.AddCommand(formattingCmd)
}
