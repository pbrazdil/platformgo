// Command portverify validates the source ledger and every completed Go port.
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
	"slices"
	"strconv"
	"strings"
)

const (
	platformRevision = "50141367492be46ebf5623f6191a14b94af2f2bd"
	nautilusRevision = "116c9b5159ebeb6b578b737d72298cac8d723723"
)

var allowedStatuses = []string{
	"discovered",
	"in-progress",
	"ported-failing",
	"ported-green",
	"conflict",
	"deferred-live",
	"not-applicable",
}

type row struct {
	sourceRepo     string
	sourceRevision string
	sourceFile     string
	sourceTest     string
	sourceLine     int
	goFile         string
	goTest         string
	category       string
	status         string
	notes          string
}

type goTest struct {
	doc string
}

func main() {
	var ledger string
	var root string
	flag.StringVar(&ledger, "ledger", "ports/test-port-map.csv", "path to the port ledger")
	flag.StringVar(&root, "root", ".", "repository root")
	flag.Parse()

	rows, err := readLedger(filepath.Join(root, ledger))
	if err != nil {
		fail(err)
	}
	if err := verify(root, rows); err != nil {
		fail(err)
	}

	counts := make(map[string]int)
	for _, row := range rows {
		counts[row.status]++
	}
	fmt.Printf(
		"verified %d rows: %d green, %d failing, %d discovered, %d not applicable\n",
		len(rows),
		counts["ported-green"],
		counts["ported-failing"],
		counts["discovered"],
		counts["not-applicable"],
	)
}

func readLedger(path string) ([]row, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, errors.New("ledger is empty")
	}

	index := make(map[string]int, len(records[0]))
	for column, name := range records[0] {
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
		"category",
		"status",
		"notes",
	}
	for _, name := range required {
		if _, ok := index[name]; !ok {
			return nil, fmt.Errorf("ledger is missing column %q", name)
		}
	}

	rows := make([]row, 0, len(records)-1)
	for recordIndex, record := range records[1:] {
		if len(record) != len(records[0]) {
			return nil, fmt.Errorf("ledger row %d has %d columns, want %d", recordIndex+2, len(record), len(records[0]))
		}
		sourceLine, err := strconv.Atoi(record[index["source_line"]])
		if err != nil || sourceLine < 1 {
			return nil, fmt.Errorf("ledger row %d has invalid source_line %q", recordIndex+2, record[index["source_line"]])
		}
		rows = append(rows, row{
			sourceRepo:     record[index["source_repo"]],
			sourceRevision: record[index["source_revision"]],
			sourceFile:     record[index["source_file"]],
			sourceTest:     record[index["source_test"]],
			sourceLine:     sourceLine,
			goFile:         record[index["go_file"]],
			goTest:         record[index["go_test"]],
			category:       record[index["category"]],
			status:         record[index["status"]],
			notes:          record[index["notes"]],
		})
	}
	return rows, nil
}

func verify(root string, rows []row) error {
	var problems []string
	seenSources := make(map[string]struct{}, len(rows))
	goFiles := make(map[string]map[string]goTest)

	for _, row := range rows {
		sourceKey := fmt.Sprintf("%s:%s:%d", row.sourceRepo, row.sourceFile, row.sourceLine)
		if _, duplicate := seenSources[sourceKey]; duplicate {
			problems = append(problems, sourceKey+": duplicate source location")
		}
		seenSources[sourceKey] = struct{}{}

		wantRevision := map[string]string{
			"platform": platformRevision,
			"nautilus": nautilusRevision,
		}[row.sourceRepo]
		if wantRevision == "" {
			problems = append(problems, sourceKey+": unknown source_repo "+row.sourceRepo)
		} else if row.sourceRevision != wantRevision {
			problems = append(problems, sourceKey+": source revision does not match the pin")
		}
		if !slices.Contains(allowedStatuses, row.status) {
			problems = append(problems, sourceKey+": invalid status "+row.status)
		}

		switch row.status {
		case "ported-green", "ported-failing":
			if row.goFile == "" || row.goTest == "" {
				problems = append(problems, sourceKey+": ported row is missing go_file or go_test")
				continue
			}
			if !strings.HasSuffix(row.goFile, "_test.go") {
				problems = append(problems, sourceKey+": go_file is not a native _test.go file")
			}

			tests, ok := goFiles[row.goFile]
			if !ok {
				var err error
				tests, err = parseGoTests(filepath.Join(root, filepath.FromSlash(row.goFile)))
				if err != nil {
					problems = append(problems, sourceKey+": "+err.Error())
					continue
				}
				goFiles[row.goFile] = tests
			}
			test, ok := tests[row.goTest]
			if !ok {
				problems = append(problems, sourceKey+": Go test "+row.goTest+" not found in "+row.goFile)
				continue
			}
			for _, expected := range []string{
				row.sourceRevision,
				fmt.Sprintf("%s:%d", row.sourceFile, row.sourceLine),
				"test: " + row.sourceTest,
			} {
				if !strings.Contains(test.doc, expected) {
					problems = append(problems, sourceKey+": "+row.goTest+" provenance is missing "+expected)
				}
			}

			if row.category == "unit" || row.category == "model" {
				data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(row.goFile)))
				if err == nil && strings.Contains(string(data), "time.Sleep(") {
					problems = append(problems, sourceKey+": unit/model test file uses time.Sleep")
				}
			}
		case "not-applicable", "conflict", "deferred-live":
			if strings.TrimSpace(row.notes) == "" {
				problems = append(problems, sourceKey+": "+row.status+" requires an explanatory note")
			}
		case "discovered":
			if row.goFile != "" || row.goTest != "" {
				problems = append(problems, sourceKey+": discovered row already names a Go port")
			}
		}
	}

	if len(problems) > 0 {
		slices.Sort(problems)
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

func parseGoTests(path string) (map[string]goTest, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	tests := make(map[string]goTest)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || !strings.HasPrefix(function.Name.Name, "Test") {
			continue
		}
		doc := ""
		if function.Doc != nil {
			doc = function.Doc.Text()
		}
		tests[function.Name.Name] = goTest{doc: doc}
	}
	return tests, nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
