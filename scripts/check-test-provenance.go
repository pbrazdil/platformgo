// Command check-test-provenance binds each port-ledger provenance record to
// the documentation attached to its exact Go test FuncDecl.
package main

import (
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type provenanceRow struct {
	line           int
	sourceRepo     string
	sourceRevision string
	sourceFile     string
	sourceTest     string
	sourceLine     string
	goFile         string
	goTest         string
}

type parsedTest struct {
	doc            string
	validSignature bool
}

func main() {
	root := flag.String("root", ".", "repository root")
	ledger := flag.String("ledger", "ports/test-port-map.csv", "port ledger path")
	sourcePolicy := flag.String(
		"source-policy",
		"ports/SOURCE_REVISIONS.md",
		"canonical source-revision policy path",
	)
	flag.Parse()

	rows, err := readPortedRows(filepath.Join(*root, filepath.FromSlash(*ledger)))
	if err != nil {
		fail(err)
	}
	repositories, err := readSourceRepositories(
		filepath.Join(*root, filepath.FromSlash(*sourcePolicy)),
	)
	if err != nil {
		fail(err)
	}
	if err := checkProvenance(*root, rows, repositories); err != nil {
		fail(err)
	}
}

func readSourceRepositories(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read source policy: %w", err)
	}
	values := make(map[string]string)
	for line := range strings.Lines(string(data)) {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && key != "" && value != "" {
			values[key] = value
		}
	}
	definitions := map[string]string{
		"platform": "PLATFORM_SOURCE_REPOSITORY",
		"nautilus": "NAUTILUS_SOURCE_REPOSITORY",
	}
	repositories := make(map[string]string, len(definitions))
	for alias, key := range definitions {
		repository := values[key]
		if repository == "" {
			return nil, fmt.Errorf("%s is missing %s", path, key)
		}
		repositories[alias] = repository
	}
	return repositories, nil
}

func readPortedRows(path string) ([]provenanceRow, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()

	reader := csv.NewReader(file)
	header, err := reader.Read()
	if err != nil {
		return nil, err
	}
	index := make(map[string]int, len(header))
	for column, name := range header {
		index[name] = column
	}
	required := []string{
		"source_repo",
		"source_revision",
		"source_file",
		"source_test",
		"source_line",
		"go_file",
		"go_test",
		"port_status",
	}
	for _, name := range required {
		if _, ok := index[name]; !ok {
			return nil, fmt.Errorf("ledger is missing column %q", name)
		}
	}

	var rows []provenanceRow
	for line := 2; ; line++ {
		record, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("line %d: %w", line, readErr)
		}
		if record[index["port_status"]] != "ported" {
			continue
		}
		rows = append(rows, provenanceRow{
			line:           line,
			sourceRepo:     record[index["source_repo"]],
			sourceRevision: record[index["source_revision"]],
			sourceFile:     record[index["source_file"]],
			sourceTest:     record[index["source_test"]],
			sourceLine:     record[index["source_line"]],
			goFile:         record[index["go_file"]],
			goTest:         record[index["go_test"]],
		})
	}
	return rows, nil
}

func checkProvenance(
	root string,
	rows []provenanceRow,
	repositories map[string]string,
) error {
	cache := make(map[string]map[string]parsedTest)
	var problems []string

	for _, row := range rows {
		tests, ok := cache[row.goFile]
		if !ok {
			path := filepath.Join(root, filepath.FromSlash(row.goFile))
			var err error
			tests, err = parseTests(path)
			if err != nil {
				problems = append(problems, fmt.Sprintf("line %d: %v", row.line, err))
				continue
			}
			cache[row.goFile] = tests
		}
		test, ok := tests[row.goTest]
		if !ok {
			problems = append(
				problems,
				fmt.Sprintf("line %d: Go file lacks test function %s", row.line, row.goTest),
			)
			continue
		}
		if !test.validSignature {
			problems = append(
				problems,
				fmt.Sprintf(
					"line %d: %s is not a valid func(*testing.T) Go test",
					row.line,
					row.goTest,
				),
			)
			continue
		}
		if !hasExactProvenance(test.doc, row, repositories[row.sourceRepo]) {
			problems = append(
				problems,
				fmt.Sprintf(
					"line %d: %s lacks exact source provenance in its attached documentation",
					row.line,
					row.goTest,
				),
			)
		}
	}

	if len(problems) == 0 {
		return nil
	}
	slices.Sort(problems)
	return errors.New(strings.Join(problems, "\n"))
}

func parseTests(path string) (map[string]parsedTest, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	testingImports := make(map[string]struct{})
	for _, imported := range file.Imports {
		importPath, unquoteErr := strconv.Unquote(imported.Path.Value)
		if unquoteErr != nil {
			return nil, fmt.Errorf("parse import in %s: %w", path, unquoteErr)
		}
		if importPath != "testing" {
			continue
		}
		name := "testing"
		if imported.Name != nil {
			name = imported.Name.Name
		}
		testingImports[name] = struct{}{}
	}
	tests := make(map[string]parsedTest)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv != nil || !strings.HasPrefix(function.Name.Name, "Test") {
			continue
		}
		doc := ""
		if function.Doc != nil {
			doc = function.Doc.Text()
		}
		tests[function.Name.Name] = parsedTest{
			doc:            doc,
			validSignature: isTestingSignature(function, testingImports),
		}
	}
	return tests, nil
}

func isTestingSignature(
	function *ast.FuncDecl,
	testingImports map[string]struct{},
) bool {
	if function.Type.TypeParams != nil && len(function.Type.TypeParams.List) != 0 {
		return false
	}
	if function.Type.Results != nil && len(function.Type.Results.List) != 0 {
		return false
	}
	if function.Type.Params == nil || len(function.Type.Params.List) != 1 {
		return false
	}
	parameter := function.Type.Params.List[0]
	if len(parameter.Names) > 1 {
		return false
	}
	pointer, ok := parameter.Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	if identifier, identifierOK := pointer.X.(*ast.Ident); identifierOK && identifier.Name == "T" {
		_, dotImported := testingImports["."]
		return dotImported
	}
	selector, ok := pointer.X.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "T" {
		return false
	}
	owner, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	_, imported := testingImports[owner.Name]
	return imported
}

func hasExactProvenance(
	doc string,
	row provenanceRow,
	repository string,
) bool {
	lines := make(map[string]struct{})
	for line := range strings.Lines(doc) {
		lines[strings.TrimSpace(line)] = struct{}{}
	}

	legacy := map[string]string{
		"platform": "platform",
		"nautilus": "NautilusTrader",
	}[row.sourceRepo]
	_, canonicalRepository := lines["repository: "+repository+"@"+row.sourceRevision]
	_, legacyRepository := lines[legacy+": "+row.sourceRevision]
	_, labeledRepository := lines[legacy+": "+repository+"@"+row.sourceRevision]
	_, source := lines["source: "+row.sourceFile+":"+row.sourceLine]
	_, test := lines["test: "+row.sourceTest]
	return (canonicalRepository || legacyRepository || labeledRepository) && source && test
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
