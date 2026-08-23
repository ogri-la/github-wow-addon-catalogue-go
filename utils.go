package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"
	"sort"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// cannot continue, exit immediately without a stacktrace.
// just use `panic` if you do need a stracktrace.
func fatal() {
	fmt.Printf("cannot continue, ") // "cannot continue, exit status 1"
	os.Exit(1)
}

// when `b` is true, logs `msg` at ERROR and exits with status 1.
// does nothing when `b` is false.
func die(b bool, msg string) {
	if b {
		slog.Error(msg)
		fatal()
	}
}

// assert `b` is true, otherwise panic with message `msg`.
func ensure(b bool, msg string) {
	if !b {
		panic(msg)
	}
}

// returns `true` if tests are being run.
func is_testing() bool {
	// https://stackoverflow.com/questions/14249217/how-do-i-know-im-running-within-go-test
	return strings.HasSuffix(os.Args[0], ".test")
}

// divides `a` by `b`, rounding up.
// a negative result is zero: fewer than none of something is still none.
func ceil_div(a, b int) int {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

// "title case" => "Title Case"
// `strings.ToTitle` behaves strangely and isn't safe with unicode.
func title_case(s string) string {
	caser := cases.Title(language.English)
	return caser.String(s)
}

// returns just the unique items in `list`.
// order is preserved.
func unique[T comparable](list []T) []T {
	idx := make(map[T]bool)
	var result []T
	for _, item := range list {
		_, present := idx[item]
		if !present {
			idx[item] = true
			result = append(result, item)
		}
	}
	return result
}

// takes N lists of things `T` and returns a single list of them.
func flatten[T any](tll ...[]T) []T {
	final_tl := []T{}
	for _, tl := range tll {
		final_tl = append(final_tl, tl...)
	}
	return final_tl
}

// pretty-prints a json `blob`.
// a blob that is not a json object returns "null", the error is not reported.
func quick_json(blob string) string {
	var foo map[string]any
	json.Unmarshal([]byte(blob), &foo)

	b, err := json.MarshalIndent(foo, "", "\t")
	if err != nil {
		slog.Error("failed to coerce string blob to json", "blob", blob, "error", err)
		fatal()
	}
	return string(b)
}

// pretty-print any `thing`.
func pprint(thing any) {
	s, _ := json.MarshalIndent(thing, "", "\t")
	fmt.Println(string(s))
}

// shortens `s` to `max_len` characters, adding a "..." suffix.
// the result may be up to three characters longer than `max_len`.
// slices by byte, so multi-byte characters at the boundary are split.
func truncate(s string, max_len int) string {
	if len(s) <= max_len {
		return s
	}
	return s[:max_len] + "..."
}

// returns `true` when `path` exists.
// errors other than 'not exists', like a permissions error, also return `true`.
func path_exists(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, os.ErrNotExist)
}

// detect if a string has a byte-order mark,
// removing it and returning the remaining bytes if so.
// returns an error if bytes cannot be read.
// - https://stackoverflow.com/questions/21371673/reading-files-with-a-bom-in-go#answer-21375405
func elide_bom(b []byte) ([]byte, error) {
	br := bytes.NewReader(b)
	r, _, err := br.ReadRune()
	if err != nil {
		return b, err
	}
	if r != '\uFEFF' {
		br.UnreadRune() // Not a BOM -- put the rune back
	}
	return io.ReadAll(br)
}

func keys[K comparable, V any](some_map map[K]V) []K {
	key_list := []K{}
	for key, _ := range some_map {
		key_list = append(key_list, key)
	}
	return key_list
}

// returns `true` when `a` and `b` hold the same items, ignoring order.
// duplicates are significant: ["a", "a"] and ["a", "b"] are not equal.
// neither argument is modified.
// - https://stackoverflow.com/questions/36000487/check-for-equality-on-slices-without-order#answer-36001228
func array_sorted_equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	a_copy := make([]string, len(a))
	b_copy := make([]string, len(b))

	copy(a_copy, a)
	copy(b_copy, b)

	sort.Strings(a_copy)
	sort.Strings(b_copy)

	return reflect.DeepEqual(a_copy, b_copy)
}
