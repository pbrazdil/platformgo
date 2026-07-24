package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseGoTestsCapturesProvenance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "example_test.go")
	source := `package example

import "testing"

// Ported from:
//   NautilusTrader: revision
//   source: source.rs:10
//   test: source_test
func TestPort(t *testing.T) {}
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	tests, err := parseGoTests(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := tests["TestPort"].doc; got == "" {
		t.Fatal("provenance comment was not captured")
	}
}
