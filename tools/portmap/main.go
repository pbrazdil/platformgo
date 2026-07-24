// Command portmap updates ledger rows from provenance comments attached to
// native Go test functions.
package main

import (
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	sourcePattern   = regexp.MustCompile(`(?m)^\s*source:\s+(.+):([0-9]+)\s*$`)
	testPattern     = regexp.MustCompile(`(?m)^\s*test:\s+([A-Za-z_][A-Za-z0-9_]*)\s*$`)
	revisionPattern = regexp.MustCompile(`(?m)^\s*(?:platform|NautilusTrader):\s+([0-9a-f]{40})\s*$`)
)

type provenance struct {
	revision string
	source   string
	line     string
	test     string
	goFile   string
	goTest   string
}

func main() {
	var ledger string
	var status string
	flag.StringVar(&ledger, "ledger", "ports/test-port-map.csv", "path to the port ledger")
	flag.StringVar(&status, "status", "", "ported-failing or ported-green")
	flag.Parse()

	if status != "ported-failing" && status != "ported-green" {
		fail(errors.New("-status must be ported-failing or ported-green"))
	}
	if flag.NArg() == 0 {
		fail(errors.New("at least one Go _test.go file is required"))
	}

	var ports []provenance
	for _, path := range flag.Args() {
		found, err := readProvenance(path)
		if err != nil {
			fail(err)
		}
		ports = append(ports, found...)
	}
	if len(ports) == 0 {
		fail(errors.New("no provenance comments found"))
	}

	updated, err := updateLedger(ledger, status, ports)
	if err != nil {
		fail(err)
	}
	fmt.Printf("updated %d ledger rows to %s\n", updated, status)
}

func readProvenance(path string) ([]provenance, error) {
	if !strings.HasSuffix(path, "_test.go") {
		return nil, fmt.Errorf("%s is not a _test.go file", path)
	}
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var found []provenance
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || !strings.HasPrefix(function.Name.Name, "Test") || function.Doc == nil {
			continue
		}
		doc := function.Doc.Text()
		sourceMatch := sourcePattern.FindStringSubmatch(doc)
		testMatch := testPattern.FindStringSubmatch(doc)
		revisionMatch := revisionPattern.FindStringSubmatch(doc)
		matches := 0
		if len(sourceMatch) > 0 {
			matches++
		}
		if len(testMatch) > 0 {
			matches++
		}
		if len(revisionMatch) > 0 {
			matches++
		}
		if matches == 0 {
			continue
		}
		if matches != 3 {
			return nil, fmt.Errorf("%s:%s has incomplete provenance", path, function.Name.Name)
		}
		found = append(found, provenance{
			revision: revisionMatch[1],
			source:   sourceMatch[1],
			line:     sourceMatch[2],
			test:     testMatch[1],
			goFile:   filepath.ToSlash(path),
			goTest:   function.Name.Name,
		})
	}
	return found, nil
}

func updateLedger(path, status string, ports []provenance) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	closeErr := file.Close()
	if err != nil {
		return 0, err
	}
	if closeErr != nil {
		return 0, closeErr
	}
	if len(records) == 0 {
		return 0, errors.New("ledger is empty")
	}

	index := make(map[string]int)
	for column, name := range records[0] {
		index[name] = column
	}
	required := []string{
		"source_revision", "source_file", "source_test", "source_line",
		"go_file", "go_test", "status",
	}
	for _, name := range required {
		if _, ok := index[name]; !ok {
			return 0, fmt.Errorf("ledger is missing column %q", name)
		}
	}

	rowsBySource := make(map[string]int, len(records)-1)
	for rowIndex, record := range records[1:] {
		key := sourceKey(
			record[index["source_revision"]],
			record[index["source_file"]],
			record[index["source_line"]],
			record[index["source_test"]],
		)
		if _, duplicate := rowsBySource[key]; duplicate {
			return 0, fmt.Errorf("duplicate ledger source %s", key)
		}
		rowsBySource[key] = rowIndex + 1
	}

	seenPorts := make(map[string]struct{}, len(ports))
	for _, port := range ports {
		if _, err := strconv.Atoi(port.line); err != nil {
			return 0, fmt.Errorf("%s:%s has invalid source line %q", port.goFile, port.goTest, port.line)
		}
		key := sourceKey(port.revision, port.source, port.line, port.test)
		if _, duplicate := seenPorts[key]; duplicate {
			return 0, fmt.Errorf("duplicate Go provenance for %s", key)
		}
		seenPorts[key] = struct{}{}

		rowIndex, ok := rowsBySource[key]
		if !ok {
			return 0, fmt.Errorf("%s:%s does not match a ledger row (%s)", port.goFile, port.goTest, key)
		}
		record := records[rowIndex]
		existingFile := record[index["go_file"]]
		existingTest := record[index["go_test"]]
		if (existingFile != "" && existingFile != port.goFile) ||
			(existingTest != "" && existingTest != port.goTest) {
			return 0, fmt.Errorf("%s is already assigned to %s:%s", key, existingFile, existingTest)
		}
		record[index["go_file"]] = port.goFile
		record[index["go_test"]] = port.goTest
		record[index["status"]] = status
	}

	temporary, err := os.CreateTemp(filepath.Dir(path), ".test-port-map-*.csv")
	if err != nil {
		return 0, err
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	writer := csv.NewWriter(temporary)
	if err := writer.WriteAll(records); err != nil {
		_ = temporary.Close()
		return 0, err
	}
	if err := writer.Error(); err != nil {
		_ = temporary.Close()
		return 0, err
	}
	if err := temporary.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return 0, err
	}
	removeTemporary = false
	return len(ports), nil
}

func sourceKey(revision, source, line, test string) string {
	return strings.Join([]string{revision, source, line, test}, ":")
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
