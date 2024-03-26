package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_is_toc_file(t *testing.T) {
	cases := map[string]bool{
		"":                  false,
		"-_!@#$%^&*(":       false, // gibberish
		"Foo":               false, // top level file
		"Foo/Bar":           false, // no '.toc'
		"Foo/Bar.toc":       false, // 'Foo' must match 'Bar'
		"Foo/Foo.toc":       true,
		"Foo/Foo-wrath.toc": true,

		// case insensitive
		"Foo/Foo-WRATH.toc": true,
		"Foo/Foo-WrAtH.ToC": true,
	}
	for given, expected := range cases {
		assert.Equal(t, expected, is_toc_file(given), given)
	}
}
