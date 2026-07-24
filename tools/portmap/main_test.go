package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadProvenance(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "price_test.go")
	source := `package decimal

import "testing"

// Ported from:
//   NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//   source: crates/model/src/types/price.rs:100
//   test: test_price
func TestPrice(t *testing.T) {}
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	found, err := readProvenance(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("readProvenance() returned %d ports, want 1", len(found))
	}
	if found[0].source != "crates/model/src/types/price.rs" ||
		found[0].line != "100" ||
		found[0].test != "test_price" {
		t.Fatalf("unexpected provenance: %+v", found[0])
	}
}
