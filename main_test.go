package main

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_parse_toc_filename(t *testing.T) {
	cases := map[string][]string{
		"Foo.toc":          {"Foo", ""},
		"Foo-mainline.toc": {"Foo", MainlineFlavor},
		"Foo_mainline.toc": {"Foo", MainlineFlavor},
		//"Foo.mainline.toc": {"Foo", MainlineFlavor}, // todo: this *should* work

		"Foo-classic.toc": {"Foo", ClassicFlavor},
		"Foo-bcc.toc":     {"Foo", BCCFlavor},
		"Foo-wrath.toc":   {"Foo", WrathFlavor},
		"Foo-cata.toc":    {"Foo", CataFlavor},

		"Foo-1.0.toc":          {"Foo-1.0", ""},
		"Foo-1.0-mainline.toc": {"Foo-1.0", MainlineFlavor},
		"Foo-1.0_mainline.toc": {"Foo-1.0", MainlineFlavor},
		//"Foo-1.0.mainline.toc": {"Foo-1.0", MainlineFlavor}, // todo: this *should* work

		"Foo-1.0-classic.toc": {"Foo-1.0", ClassicFlavor},
		"Foo-1.0-vanilla.toc": {"Foo-1.0", ClassicFlavor},
		"Array_Vanilla.toc":   {"Array", ClassicFlavor},

		"Foo-1.0-bcc.toc": {"Foo-1.0", BCCFlavor},
		"Foo-1.0-tbc.toc": {"Foo-1.0", BCCFlavor},

		"Foo-1.0-wrath.toc":  {"Foo-1.0", WrathFlavor},
		"Foo-1.0-wotlk.toc":  {"Foo-1.0", WrathFlavor},
		"Foo-1.0-wotlkc.toc": {"Foo-1.0", WrathFlavor},

		"Foo-1.0-cata.toc": {"Foo-1.0", CataFlavor},
	}
	for given, expected := range cases {
		actual_filename, actual_flavor := parse_toc_filename(given)
		expected_filename, expected_flavor := expected[0], expected[1]
		assert.Equal(t, expected_filename, actual_filename)
		assert.Equal(t, expected_flavor, actual_flavor)
	}
}

func Test_is_toc_file(t *testing.T) {
	cases := map[string]bool{
		"":                         false,
		"-_!@#$%^&*(":              false, // gibberish
		"Foo":                      false, // top level file
		"Foo/Bar":                  false, // no '.toc'
		"Foo/Bar.toc":              false, // 'Foo' must match 'Bar'
		"Foo/foo.toc":              true,  // 'Foo' matches 'foo' ignoring case.
		"Foo/fOo.toc":              true,  // 'Foo' matches 'fOo' ignoring case.
		"Foo/Foo.toc":              true,
		"Foo/Foo-wrath.toc":        true,
		"Foo/Foo_wrath.toc":        true,
		"Foo-1.0/Foo-1.0.toc":      true,
		"Foo-1.0/Foo-1.0-cata.toc": true,
		"Foo-1.0/Foo-1.0_cata.toc": true,

		// case insensitive
		"Foo/Foo-WRATH.toc": true,
		"Foo/Foo-WrAtH.ToC": true,
	}
	for given, expected := range cases {
		assert.Equal(t, expected, is_toc_file(given), given)
	}
}

func Test_is_excluded(t *testing.T) {
	cases := map[string]bool{
		"":           false, // matches nothing
		"foo":        false,
		"foo/":       true, // matches 'foo/'
		"foo/bar":    true, // matches 'foo/'
		"foo/barbaz": true, // matches 'foo/'
	}
	blacklist := map[string]bool{
		"foo/": true,
	}
	var filter *regexp.Regexp
	for repo_fullname, expected := range cases {
		_, actual := is_excluded(blacklist, filter, repo_fullname)
		assert.Equal(t, expected, actual)
	}
}

func Test_title_case(t *testing.T) {
	cases := map[string]string{
		"":           "",
		"title case": "Title Case",
		"Title case": "Title Case",
		"Title Case": "Title Case",
		"title-case": "Title-Case",
		"title_case": "Title_case",
		"TITLE CASE": "Title Case",
	}
	for given, expected := range cases {
		assert.Equal(t, expected, title_case(given))
	}
}

func Test_interface_number_to_flavor(t *testing.T) {
	cases := map[string]Flavor{
		"10000": MainlineFlavor,
		"13000": ClassicFlavor,
		"20000": MainlineFlavor,
		"20500": BCCFlavor,
		"30000": MainlineFlavor,
		"30400": WrathFlavor,
		"30403": WrathFlavor,
		"40000": MainlineFlavor,
		"40400": CataFlavor,
		"50000": MainlineFlavor,
	}
	for interface_number, expected := range cases {
		actual, err := interface_number_to_flavor(interface_number)
		assert.Nil(t, err)
		assert.Equal(t, expected, actual)
	}
}
