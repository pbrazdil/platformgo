package main

import (
	"os"
	"path/filepath"
	"strings"
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

func TestVerifyRejectsUnassignedNativeTest(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "internal", "example", "example_test.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`package example

import "testing"

func TestUnassigned(t *testing.T) {}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	err := verify(root, nil)
	if err == nil || !strings.Contains(err.Error(), "native test is not assigned to a ported ledger row") {
		t.Fatalf("verify error = %v", err)
	}
}

func TestVerifyRejectsMultipleSourcesAssignedToOneNativeTest(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "internal", "example", "example_test.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`package example

import "testing"

// Ported from:
//   NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//   source: one.rs:10
//   test: first
func TestShared(t *testing.T) {}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	rows := []row{
		{
			sourceRepo: "nautilus", sourceRevision: nautilusRevision,
			sourceFile: "one.rs", sourceTest: "first", sourceLine: 10,
			goFile: "internal/example/example_test.go", goTest: "TestShared",
			category: "unit", status: "ported-green",
		},
		{
			sourceRepo: "nautilus", sourceRevision: nautilusRevision,
			sourceFile: "one.rs", sourceTest: "second", sourceLine: 20,
			goFile: "internal/example/example_test.go", goTest: "TestShared",
			category: "unit", status: "ported-green",
		},
	}
	err := verify(root, rows)
	if err == nil || !strings.Contains(err.Error(), "native test is assigned to multiple ledger rows") {
		t.Fatalf("verify error = %v", err)
	}
}
