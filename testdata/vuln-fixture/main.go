// Package main is a deliberately vulnerable fixture for the standing
// govulncheck policy gate (B19 / issue #307). It imports and calls
// golang.org/x/net/html at a version with published OSV entries so the
// policy command can be proven to fail on known-bad input.
//
// This module is self-contained (own go.mod) and must never be pulled into
// the parent factvault module's build or test graph.
package main

import (
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

func main() {
	doc, err := html.Parse(strings.NewReader("<html><body>fixture</body></html>"))
	if err != nil {
		panic(err)
	}
	var b strings.Builder
	if err := html.Render(&b, doc); err != nil {
		panic(err)
	}
	fmt.Println(b.String())
}
