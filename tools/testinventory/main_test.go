package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTestsInFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tests.rs")
	source := `#[tokio::test(flavor = "multi_thread")]
async fn async_case() {}

#[ignore = "live"]
#[test]
fn ignored_case() {}

#[rstest]
#[case("one")]
fn parameterized_case(#[case] input: &str) {}

#[rstest]
#[case(
    "multi-line",
    true
)]
fn multiline_attribute_case(#[case] input: &str, #[case] expected: bool) {}

fn helper() {}
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := testsInFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []sourceTest{
		{name: "async_case", line: 2},
		{name: "ignored_case", line: 6},
		{name: "parameterized_case", line: 10},
		{name: "multiline_attribute_case", line: 17},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("testsInFile() = %v, want %v", got, want)
	}
}

func TestClassify(t *testing.T) {
	tests := map[string]string{
		"apps/app/tests/it/messaging/e2e_outbox.rs":       "integration-messaging",
		"apps/app/tests/it/realtime/e2e_gateway.rs":       "contract-realtime",
		"apps/nautilus/tests/it/recovery/e2e_recovery.rs": "integration-postgres",
		"apps/nautilus/tests/live/trading/e2e_order.rs":   "model",
		"crates/model/src/orders/limit.rs":                "unit",
	}
	for path, want := range tests {
		t.Run(path, func(t *testing.T) {
			if got := classify(path); got != want {
				t.Fatalf("classify(%q) = %q, want %q", path, got, want)
			}
		})
	}
}
