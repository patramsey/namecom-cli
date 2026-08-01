package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func load(p string) any {
	b, err := os.ReadFile(p)
	if err != nil {
		panic(err)
	}
	var m any
	if err := yaml.Unmarshal(b, &m); err != nil {
		panic(fmt.Sprintf("%s does not parse: %v", p, err))
	}
	return m
}

var diffs []string

func note(f string, a ...any) { diffs = append(diffs, fmt.Sprintf(f, a...)) }

func walk(a, b any, path string) {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			note("%s: map became %T", path, b)
			return
		}
		keys := map[string]bool{}
		for k := range av {
			keys[k] = true
		}
		for k := range bv {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			x, inA := av[k]
			y, inB := bv[k]
			switch {
			case inA && !inB:
				note("%s/%s: REMOVED (%v)", path, k, trunc(x))
			case !inA && inB:
				note("%s/%s: ADDED (%v)", path, k, trunc(y))
			default:
				walk(x, y, path+"/"+k)
			}
		}
	case []any:
		bv, ok := b.([]any)
		if !ok {
			note("%s: list became %T", path, b)
			return
		}
		if len(av) != len(bv) {
			note("%s: list length %d -> %d", path, len(av), len(bv))
			return
		}
		for i := range av {
			walk(av[i], bv[i], fmt.Sprintf("%s[%d]", path, i))
		}
	default:
		if fmt.Sprint(a) != fmt.Sprint(b) {
			note("%s: %v -> %v", path, trunc(a), trunc(b))
		}
	}
}

func trunc(v any) string {
	s := strings.ReplaceAll(fmt.Sprint(v), "\n", " ")
	if len(s) > 70 {
		s = s[:70] + "…"
	}
	return s
}

func main() {
	walk(load(os.Args[1]), load(os.Args[2]), "")

	expected, unexpected := 0, []string{}
	for _, d := range diffs {
		switch {
		case strings.HasPrefix(d, "/openapi:"),
			strings.Contains(d, "/type: ["),
			strings.Contains(d, "nullable: ADDED"),
			strings.Contains(d, "Response"),
			strings.Contains(d, "oneOf: list length"),
			strings.Contains(d, "anyOf: list length"):
			expected++
		default:
			unexpected = append(unexpected, d)
		}
	}
	fmt.Printf("total differences: %d\n", len(diffs))
	fmt.Printf("  attributable to a documented transform: %d\n", expected)
	fmt.Printf("  UNEXPLAINED: %d\n", len(unexpected))
	for i, d := range unexpected {
		if i >= 25 {
			fmt.Printf("  … and %d more\n", len(unexpected)-25)
			break
		}
		fmt.Println("   !!", d)
	}
}
