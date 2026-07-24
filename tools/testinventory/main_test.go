package main

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const testPlatformRevision = "50141367492be46ebf5623f6191a14b94af2f2bd"

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

func TestWriteCSVUsesSeparatedLifecycleColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ports", "test-port-map.csv")
	tests := []test{{
		repo:     "platform",
		revision: testPlatformRevision,
		file:     "apps/app/tests/it/order.rs",
		name:     "test_submit_order",
		line:     12,
		category: "model",
	}}

	if err := writeCSV(path, tests); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	wantHeader := []string{
		"source_repo",
		"source_revision",
		"source_file",
		"source_test",
		"source_line",
		"go_file",
		"go_test",
		"category",
		"port_status",
		"review_status",
		"wiring_status",
		"evidence",
		"milestone",
		"port_owner",
		"implementation_owner",
		"notes",
	}
	if len(records) != 2 || !slices.Equal(records[0], wantHeader) {
		t.Fatalf("records = %v, want header %v and one row", records, wantHeader)
	}
	if got := records[1][8:15]; !slices.Equal(
		got,
		[]string{"discovered", "unreviewed", "placeholder", "", "", "", ""},
	) {
		t.Fatalf("lifecycle columns = %v", got)
	}
}

func TestLoadSourcePolicyUsesCanonicalRevisionDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SOURCE_REVISIONS.md")
	content := strings.Join([]string{
		"PLATFORM_SOURCE_REPOSITORY=upcomers-org/platform",
		"PLATFORM_SOURCE_COMMIT=" + testPlatformRevision,
		"PLATFORM_SOURCE_ROOTS=apps/nautilus/tests,apps/app/tests",
		"PLATFORM_SOURCE_TEST_COUNT=271",
		"NAUTILUS_SOURCE_REPOSITORY=nautechsystems/nautilus_trader",
		"NAUTILUS_SOURCE_REVISION=116c9b5159ebeb6b578b737d72298cac8d723723",
		"NAUTILUS_SOURCE_ROOTS=crates/model/src",
		"NAUTILUS_SOURCE_TEST_COUNT=2477",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	sources, err := loadSourcePolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 {
		t.Fatalf("sources = %v", sources)
	}
	if got := sources[0]; got.repo != "platform" ||
		got.revision != testPlatformRevision ||
		!slices.Equal(got.scopes, []string{"apps/nautilus/tests", "apps/app/tests"}) ||
		got.expectedCount != 271 {
		t.Fatalf("platform source = %+v", got)
	}
}
