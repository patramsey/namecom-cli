package cmdutil

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// pflag parses integer flags with strconv base 0, so `--limit 010` was read
// as octal and sent perPage=8, and `--ttl 0o777`, `--ttl 1_000` and
// `--order-id 0x10` were all taken (#381). DecimalIntFlags makes every
// integer flag in the tree decimal by wrapping its Value: the wrapper refuses
// a value base 0 would read in another base, or with underscores, and hands
// anything else, a leading sign included, to the flag's own parser.

// notDecimalError is a wrapped integer flag's refusal; invalidValue turns it
// into the usage message, naming the element of a list that was refused.
type notDecimalError struct{ value string }

func (e *notDecimalError) Error() string { return "not a decimal number: " + e.value }

// decimalTypes are the pflag types DecimalIntFlags wraps, true for a list.
// count is left out: it takes no value of its own.
var decimalTypes = map[string]bool{
	"int": false, "int8": false, "int16": false, "int32": false, "int64": false,
	"uint": false, "uint8": false, "uint16": false, "uint32": false, "uint64": false,
	"intSlice": true, "int32Slice": true, "int64Slice": true, "uintSlice": true,
}

// checkDecimal refuses v when base 0 would not read it as decimal: a leading
// zero (010 is octal, 0x10 hex, 0o7 and 0b1 likewise) or an underscore. A
// value that is not a number at all is left to the flag's own parser, so
// its message stays "must be a whole number".
func checkDecimal(v string) error {
	digits := strings.TrimLeft(v, "+-")
	if strings.Contains(v, "_") || (len(digits) > 1 && digits[0] == '0') {
		return &notDecimalError{value: v}
	}
	return nil
}

type decimalValue struct{ pflag.Value }

func (d decimalValue) Set(v string) error {
	if err := checkDecimal(v); err != nil {
		return err
	}
	return d.Value.Set(v)
}

// decimalSliceValue keeps pflag.SliceValue, which cobra's completion checks
// to tell that a flag may be repeated.
type decimalSliceValue struct {
	pflag.Value
	slice pflag.SliceValue
}

func checkDecimalList(vs []string) error {
	for _, v := range vs {
		if err := checkDecimal(strings.TrimSpace(v)); err != nil {
			return err
		}
	}
	return nil
}

func (d decimalSliceValue) Set(v string) error {
	if err := checkDecimalList(strings.Split(v, ",")); err != nil {
		return err
	}
	return d.Value.Set(v)
}

func (d decimalSliceValue) Append(v string) error {
	if err := checkDecimal(v); err != nil {
		return err
	}
	return d.slice.Append(v)
}

func (d decimalSliceValue) Replace(vs []string) error {
	if err := checkDecimalList(vs); err != nil {
		return err
	}
	return d.slice.Replace(vs)
}

func (d decimalSliceValue) GetSlice() []string { return d.slice.GetSlice() }

// DecimalIntFlags wraps every integer flag of cmd and its subcommands. It is
// run once, before the first parse; wrapping a flag twice is harmless.
func DecimalIntFlags(cmd *cobra.Command) {
	wrap := func(f *pflag.Flag) {
		switch f.Value.(type) {
		case decimalValue, decimalSliceValue:
			return
		}
		list, ok := decimalTypes[f.Value.Type()]
		if !ok {
			return
		}
		if s, isSlice := f.Value.(pflag.SliceValue); list && isSlice {
			f.Value = decimalSliceValue{Value: f.Value, slice: s}
			return
		}
		f.Value = decimalValue{f.Value}
	}
	cmd.PersistentFlags().VisitAll(wrap)
	cmd.Flags().VisitAll(wrap)
	for _, c := range cmd.Commands() {
		DecimalIntFlags(c)
	}
}
