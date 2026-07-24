package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckRejectsFloatInEconomicPackage(t *testing.T) {
	root := policyFixture(t)
	writeFixtureFile(t, root, "internal/domain/value.go", "package domain\n\ntype Amount float64\n")

	problems, err := checkRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	requireProblem(t, problems, "floating-point type float64 is forbidden in economic package")
}

func TestCheckRejectsInferredFloatInEconomicPackage(t *testing.T) {
	root := policyFixture(t)
	writeFixtureFile(t, root, "internal/domain/value.go", "package domain\n\nvar Amount = 0.1\n")

	problems, err := checkRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	requireProblem(t, problems, "floating-point literal 0.1 is forbidden in economic package")
}

func TestCheckAcceptsExplicitFloatCompatibilityFile(t *testing.T) {
	root := policyFixture(t)
	writeFixtureFile(t, root, "internal/domain/compat.go", "package domain\n\ntype LegacyValue float64\n")
	writeFixtureFile(t, root, floatCompatibilityPath, "internal/domain/compat.go\n")

	problems, err := checkRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	requireNoProblems(t, problems)
}

func TestCheckRejectsProductionPanicUnlessQuarantined(t *testing.T) {
	root := policyFixture(t)
	writeFixtureFile(t, root, "internal/domain/value.go", "package domain\n\nfunc MustValue() { panic(\"legacy\") }\n")

	problems, err := checkRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	requireProblem(t, problems, "panic is forbidden in deterministic/economic production code")

	writeFixtureFile(t, root, panicCompatibilityPath, "internal/domain/value.go\n")
	problems, err = checkRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	requireNoProblems(t, problems)
}

func TestCheckRejectsImplicitRuntimeInputs(t *testing.T) {
	root := policyFixture(t)
	writeFixtureFile(t, root, "internal/domain/value.go", `package domain

import (
	"math/rand/v2"
	"os"
	"time"
)

func Value() int {
	_ = os.Getenv("VALUE")
	_ = time.Now()
	return rand.Int()
}
`)

	problems, err := checkRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	requireProblem(t, problems, "wall-clock call time.Now is forbidden in deterministic package")
	requireProblem(t, problems, "environment call os.Getenv is forbidden in deterministic package")
	requireProblem(t, problems, "randomness call rand.Int is forbidden in deterministic production code")
}

func TestCheckAllowsSeededRandomnessInTests(t *testing.T) {
	root := policyFixture(t)
	writeFixtureFile(t, root, "internal/domain/value.go", "package domain\n")
	writeFixtureFile(t, root, "internal/domain/value_test.go", `package domain

import (
	"math/rand/v2"
	"testing"
)

func TestSeededProperty(t *testing.T) {
	random := rand.New(rand.NewPCG(1, 2))
	_ = random.Uint64()
}
`)

	problems, err := checkRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	requireNoProblems(t, problems)
}

func TestCheckRejectsImplicitRandomnessAndEnvironmentInTests(t *testing.T) {
	root := policyFixture(t)
	writeFixtureFile(t, root, "internal/domain/value_test.go", `package domain

import (
	"math/rand/v2"
	"os"
	"testing"
)

func TestNondeterministicInputs(t *testing.T) {
	_ = rand.Int()
	_ = os.Getenv("VALUE")
	t.Setenv("VALUE", "1")
}
`)

	problems, err := checkRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	requireProblem(t, problems, "randomness call rand.Int is forbidden in deterministic test")
	requireProblem(t, problems, "environment call os.Getenv is forbidden in deterministic package")
	requireProblem(t, problems, "t.Setenv is forbidden in deterministic/unit/model tests")
}

func TestCheckRejectsInfrastructureImportInDeterministicPackage(t *testing.T) {
	root := policyFixture(t)
	writeFixtureFile(t, root, "internal/domain/value.go", `package domain

import "database/sql"

var _ *sql.DB
`)

	problems, err := checkRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	requireProblem(t, problems, `infrastructure import "database/sql" is forbidden in deterministic package`)
}

func TestCheckRestrictsAPDToEconomicDecimalPackage(t *testing.T) {
	for _, sourcePath := range []string{
		"internal/domain/value.go",
		"internal/decimal/value.go",
		"internal/decimal/other/value.go",
		"internal/decimal/economic/other/value.go",
	} {
		t.Run(sourcePath, func(t *testing.T) {
			root := policyFixture(t)
			writeFixtureFile(t, root, sourcePath, `package decimal

import "github.com/cockroachdb/apd/v3"

var _ apd.Decimal
`)

			problems, err := checkRoot(root)
			if err != nil {
				t.Fatal(err)
			}
			requireProblem(t, problems, "apd/v3 may only be imported by internal/decimal/economic")
		})
	}

	root := policyFixture(t)
	writeFixtureFile(t, root, "internal/decimal/economic/value.go", `package decimal

import "github.com/cockroachdb/apd/v3"

var _ apd.Decimal
`)
	problems, err := checkRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, problem := range problems {
		if strings.Contains(problem.String(), "apd/v3") {
			t.Fatalf("production decimal import rejected: %s", problem)
		}
	}
}

func TestCheckRejectsProductionImportOfPortedCompatibilityPackage(t *testing.T) {
	root := policyFixture(t)
	writeFixtureFile(t, root, packagePolicyPath, strings.Join([]string{
		"path,classification,economic,deterministic",
		"internal/domain,production-economic,true,true",
		"internal/legacy,ported-compatibility,true,true",
		"",
	}, "\n"))
	writeFixtureFile(t, root, "internal/domain/value.go", `package domain

import _ "example.com/project/internal/legacy"
`)
	writeFixtureFile(t, root, "internal/legacy/value.go", "package legacy\n")

	problems, err := checkRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	requireProblem(t, problems, "production package must not import quarantined compatibility package")
}

func TestCheckRejectsUnsafeAndForbiddenTestControls(t *testing.T) {
	root := policyFixture(t)
	writeFixtureFile(t, root, "internal/domain/value.go", "package domain\n\nimport _ \"unsafe\"\n")
	writeFixtureFile(t, root, "internal/domain/value_test.go", `package domain

import (
	"testing"
	"time"
)

func TestValue(tb *testing.T) {
	tb.Parallel()
	tb.Skip("later")
	time.Sleep(time.Second)
}
`)

	problems, err := checkRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	requireProblem(t, problems, `unsafe import is forbidden`)
	requireProblem(t, problems, "t.Parallel is forbidden until explicit harness approval")
	requireProblem(t, problems, "t.Skip is forbidden in deterministic/unit/model tests")
	requireProblem(t, problems, "time.Sleep is forbidden in deterministic/unit/model tests")
}

func TestCheckRejectsUnclassifiedPackage(t *testing.T) {
	root := policyFixture(t)
	writeFixtureFile(t, root, "internal/unclassified/value.go", "package unclassified\n")
	writeFixtureFile(t, root, "cmd/service/main.go", "package main\n")

	problems, err := checkRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	requireProblem(t, problems, "Go package is missing from policy/go-package-policy.csv")
	requireProblemAtPath(t, problems, "cmd/service/main.go")
}

func TestCheckRejectsStaleCompatibilityEntry(t *testing.T) {
	root := policyFixture(t)
	writeFixtureFile(t, root, "internal/domain/value.go", "package domain\n")
	writeFixtureFile(t, root, floatCompatibilityPath, "internal/domain/value.go\n")

	problems, err := checkRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	requireProblem(t, problems, "float compatibility entry is stale")
}

func policyFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, root, packagePolicyPath, strings.Join([]string{
		"path,classification,economic,deterministic",
		"internal/domain,production-economic,true,true",
		"",
	}, "\n"))
	writeFixtureFile(t, root, floatCompatibilityPath, "")
	writeFixtureFile(t, root, panicCompatibilityPath, "")
	return root
}

func writeFixtureFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func requireProblem(t *testing.T, problems []problem, want string) {
	t.Helper()
	for _, candidate := range problems {
		if strings.Contains(candidate.String(), want) {
			return
		}
	}
	t.Fatalf("problems = %v, want a problem containing %q", problems, want)
}

func requireProblemAtPath(t *testing.T, problems []problem, want string) {
	t.Helper()
	for _, candidate := range problems {
		if candidate.path == want {
			return
		}
	}
	t.Fatalf("problems = %v, want a problem at %q", problems, want)
}

func requireNoProblems(t *testing.T, problems []problem) {
	t.Helper()
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
}
