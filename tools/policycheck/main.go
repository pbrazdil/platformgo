// Command policycheck enforces deterministic and economic Go package policy
// using the Go syntax tree rather than text matching.
package main

import (
	"bufio"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	packagePolicyPath        = "policy/go-package-policy.csv"
	floatCompatibilityPath   = "policy/float-compatibility-files.txt"
	panicCompatibilityPath   = "policy/panic-compatibility-files.txt"
	packagePolicyColumnCount = 4
)

var classifications = map[string]struct{}{
	"production-economic":  {},
	"ported-compatibility": {},
	"test-placeholder":     {},
	"non-economic":         {},
	"infrastructure":       {},
	"tooling":              {},
}

type packageRule struct {
	path           string
	classification string
	economic       bool
	deterministic  bool
}

type problem struct {
	path    string
	line    int
	message string
}

func (p problem) String() string {
	if p.line > 0 {
		return fmt.Sprintf("%s:%d: %s", p.path, p.line, p.message)
	}
	return fmt.Sprintf("%s: %s", p.path, p.message)
}

type checker struct {
	root               string
	rules              []packageRule
	floatCompatibility map[string]struct{}
	panicCompatibility map[string]struct{}
	usedFloat          map[string]struct{}
	usedPanic          map[string]struct{}
	problems           []problem
}

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	problems, err := checkRoot(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(problems) != 0 {
		fmt.Fprintln(os.Stderr, "Go policy validation failed:")
		for _, candidate := range problems {
			fmt.Fprintf(os.Stderr, "- %s\n", candidate)
		}
		os.Exit(1)
	}
	fmt.Println("Go package policy checks passed")
}

func checkRoot(root string) ([]problem, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}
	rules, err := loadPackageRules(filepath.Join(absoluteRoot, packagePolicyPath))
	if err != nil {
		return nil, err
	}
	floatCompatibility, err := loadCompatibilityList(
		filepath.Join(absoluteRoot, floatCompatibilityPath),
	)
	if err != nil {
		return nil, err
	}
	panicCompatibility, err := loadCompatibilityList(
		filepath.Join(absoluteRoot, panicCompatibilityPath),
	)
	if err != nil {
		return nil, err
	}

	state := checker{
		root:               absoluteRoot,
		rules:              rules,
		floatCompatibility: floatCompatibility,
		panicCompatibility: panicCompatibility,
		usedFloat:          make(map[string]struct{}),
		usedPanic:          make(map[string]struct{}),
	}
	if err := filepath.WalkDir(absoluteRoot, state.visit); err != nil {
		return nil, fmt.Errorf("scan Go files: %w", err)
	}
	state.validateCompatibilityEntries(
		floatCompatibility,
		state.usedFloat,
		"float compatibility entry is stale",
	)
	state.validateCompatibilityEntries(
		panicCompatibility,
		state.usedPanic,
		"panic compatibility entry is stale",
	)
	slices.SortFunc(state.problems, func(left, right problem) int {
		if order := strings.Compare(left.path, right.path); order != 0 {
			return order
		}
		if left.line != right.line {
			return left.line - right.line
		}
		return strings.Compare(left.message, right.message)
	})
	return state.problems, nil
}

func loadPackageRules(path string) ([]packageRule, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open package policy: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	reader := csv.NewReader(file)
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read package policy header: %w", err)
	}
	wantHeader := []string{"path", "classification", "economic", "deterministic"}
	if !slices.Equal(header, wantHeader) {
		return nil, fmt.Errorf("%s has header %v, want %v", path, header, wantHeader)
	}

	seen := make(map[string]struct{})
	var rules []packageRule
	for line := 2; ; line++ {
		record, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, readErr)
		}
		if len(record) != packagePolicyColumnCount {
			return nil, fmt.Errorf(
				"%s:%d: got %d columns, want %d",
				path,
				line,
				len(record),
				packagePolicyColumnCount,
			)
		}
		relative, normalizeErr := normalizeRelativePath(record[0])
		if normalizeErr != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, normalizeErr)
		}
		if _, duplicate := seen[relative]; duplicate {
			return nil, fmt.Errorf("%s:%d: duplicate package path %s", path, line, relative)
		}
		seen[relative] = struct{}{}

		classification := strings.TrimSpace(record[1])
		if _, ok := classifications[classification]; !ok {
			return nil, fmt.Errorf(
				"%s:%d: unknown classification %q",
				path,
				line,
				classification,
			)
		}
		economic, parseErr := strconv.ParseBool(strings.TrimSpace(record[2]))
		if parseErr != nil {
			return nil, fmt.Errorf("%s:%d: economic must be true or false", path, line)
		}
		deterministic, parseErr := strconv.ParseBool(strings.TrimSpace(record[3]))
		if parseErr != nil {
			return nil, fmt.Errorf("%s:%d: deterministic must be true or false", path, line)
		}
		rules = append(rules, packageRule{
			path:           relative,
			classification: classification,
			economic:       economic,
			deterministic:  deterministic,
		})
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("%s has no package rules", path)
	}
	slices.SortFunc(rules, func(left, right packageRule) int {
		if len(left.path) != len(right.path) {
			return len(right.path) - len(left.path)
		}
		return strings.Compare(left.path, right.path)
	})
	return rules, nil
}

func loadCompatibilityList(path string) (map[string]struct{}, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open compatibility list: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	entries := make(map[string]struct{})
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		value := strings.TrimSpace(scanner.Text())
		if value == "" || strings.HasPrefix(value, "#") {
			continue
		}
		relative, normalizeErr := normalizeRelativePath(value)
		if normalizeErr != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, normalizeErr)
		}
		if _, duplicate := entries[relative]; duplicate {
			return nil, fmt.Errorf("%s:%d: duplicate compatibility path %s", path, line, relative)
		}
		entries[relative] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return entries, nil
}

func normalizeRelativePath(value string) (string, error) {
	value = filepath.ToSlash(strings.TrimSpace(value))
	if value == "" {
		return "", errors.New("path must not be empty")
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if cleaned == "." || filepath.IsAbs(filepath.FromSlash(value)) ||
		cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("path must be repository-relative: %q", value)
	}
	return cleaned, nil
}

func (c *checker) visit(path string, entry fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if entry.IsDir() {
		switch entry.Name() {
		case ".git", ".sources", "vendor":
			if path != c.root {
				return filepath.SkipDir
			}
		}
		return nil
	}
	if filepath.Ext(path) != ".go" {
		return nil
	}

	relative, err := filepath.Rel(c.root, path)
	if err != nil {
		return err
	}
	relative = filepath.ToSlash(relative)
	directory := filepath.ToSlash(filepath.Dir(relative))
	rule, classified := c.ruleFor(directory)
	if requiresClassification(relative) && !classified {
		c.add(relative, 0, "Go package is missing from policy/go-package-policy.csv")
	}
	return c.inspectFile(path, relative, rule, classified)
}

func requiresClassification(path string) bool {
	for _, root := range []string{
		"cmd/",
		"internal/",
		"scripts/",
		"testkit/",
		"tests/",
		"tools/",
	} {
		if strings.HasPrefix(path, root) {
			return true
		}
	}
	return false
}

func (c *checker) ruleFor(directory string) (packageRule, bool) {
	for _, rule := range c.rules {
		if directory == rule.path || strings.HasPrefix(directory, rule.path+"/") {
			return rule, true
		}
	}
	return packageRule{}, false
}

func (c *checker) ruleForImport(importPath string) (packageRule, bool) {
	for _, rule := range c.rules {
		marker := "/" + rule.path
		index := strings.LastIndex(importPath, marker)
		if index < 0 {
			continue
		}
		end := index + len(marker)
		if end == len(importPath) || importPath[end] == '/' {
			return rule, true
		}
	}
	return packageRule{}, false
}

func (c *checker) inspectFile(
	path string,
	relative string,
	rule packageRule,
	classified bool,
) error {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, nil, 0)
	if err != nil {
		return fmt.Errorf("parse %s: %w", relative, err)
	}

	isTest := strings.HasSuffix(relative, "_test.go")
	imports := make(map[string]string)
	for _, imported := range file.Imports {
		importPath, unquoteErr := strconv.Unquote(imported.Path.Value)
		if unquoteErr != nil {
			return fmt.Errorf("parse import in %s: %w", relative, unquoteErr)
		}
		name := importName(imported, importPath)
		if name != "" && name != "_" && name != "." {
			imports[name] = importPath
		}
		if importPath == "unsafe" {
			c.add(relative, fileSet.Position(imported.Pos()).Line, "unsafe import is forbidden")
		}
		if classified && rule.deterministic && isInfrastructureImport(importPath) {
			c.add(
				relative,
				fileSet.Position(imported.Pos()).Line,
				fmt.Sprintf(
					"infrastructure import %q is forbidden in deterministic package",
					importPath,
				),
			)
		}
		if classified && rule.deterministic && importPath == "crypto/rand" {
			c.add(
				relative,
				fileSet.Position(imported.Pos()).Line,
				"crypto/rand import is forbidden in deterministic package",
			)
		}
		if classified && rule.deterministic && !isTest &&
			(importPath == "math/rand" || importPath == "math/rand/v2") {
			c.add(
				relative,
				fileSet.Position(imported.Pos()).Line,
				fmt.Sprintf(
					"randomness import %q is forbidden in deterministic production code",
					importPath,
				),
			)
		}
		if classified && rule.classification == "production-economic" {
			target, ok := c.ruleForImport(importPath)
			if ok && target.classification == "ported-compatibility" {
				c.add(
					relative,
					fileSet.Position(imported.Pos()).Line,
					fmt.Sprintf(
						"production package must not import quarantined compatibility package %q",
						target.path,
					),
				)
			}
		}
	}

	testingParameters := testingParameterNames(file, imports)
	floatAllowed := hasEntry(c.floatCompatibility, relative)
	panicAllowed := hasEntry(c.panicCompatibility, relative)
	hasFloat := false
	hasPanic := false

	ast.Inspect(file, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.Ident:
			if classified && rule.economic &&
				(typed.Name == "float32" || typed.Name == "float64") {
				hasFloat = true
				if !floatAllowed {
					c.add(
						relative,
						fileSet.Position(typed.Pos()).Line,
						fmt.Sprintf(
							"floating-point type %s is forbidden in economic package",
							typed.Name,
						),
					)
				}
			}
		case *ast.BasicLit:
			if classified && rule.economic && typed.Kind == token.FLOAT {
				hasFloat = true
				if !floatAllowed {
					c.add(
						relative,
						fileSet.Position(typed.Pos()).Line,
						fmt.Sprintf(
							"floating-point literal %s is forbidden in economic package",
							typed.Value,
						),
					)
				}
			}
		case *ast.CallExpr:
			if identifier, ok := typed.Fun.(*ast.Ident); ok && identifier.Name == "panic" {
				hasPanic = true
				if classified && (rule.economic || rule.deterministic) &&
					!isTest && !panicAllowed {
					c.add(
						relative,
						fileSet.Position(typed.Pos()).Line,
						"panic is forbidden in deterministic/economic production code",
					)
				}
				return true
			}

			selector, owner, ok := selectorCall(typed)
			if !ok {
				return true
			}
			importPath := imports[owner]
			line := fileSet.Position(typed.Pos()).Line
			_, isTestingParameter := testingParameters[owner]

			if isTest && isPolicyTest(relative) &&
				isTestingParameter && selector == "Parallel" {
				c.add(relative, line, "t.Parallel is forbidden until explicit harness approval")
			}
			if isTest && isInternalModelTest(relative) && isTestingParameter {
				switch {
				case strings.HasPrefix(selector, "Skip"):
					c.add(relative, line, "t.Skip is forbidden in deterministic/unit/model tests")
				case selector == "Setenv":
					c.add(relative, line, "t.Setenv is forbidden in deterministic/unit/model tests")
				}
			}
			if isTest && isInternalModelTest(relative) &&
				importPath == "time" && selector == "Sleep" {
				c.add(relative, line, "time.Sleep is forbidden in deterministic/unit/model tests")
				return true
			}

			if !classified || !rule.deterministic {
				return true
			}
			if importPath == "time" && (selector == "Now" || selector == "Sleep") {
				c.add(
					relative,
					line,
					fmt.Sprintf(
						"wall-clock call %s.%s is forbidden in deterministic package",
						owner,
						selector,
					),
				)
			}
			if importPath == "os" && isEnvironmentCall(selector) {
				c.add(
					relative,
					line,
					fmt.Sprintf(
						"environment call %s.%s is forbidden in deterministic package",
						owner,
						selector,
					),
				)
			}
			if isTest {
				if isRandomImport(importPath) &&
					!isSeededRandomConstructor(selector) {
					c.add(
						relative,
						line,
						fmt.Sprintf(
							"randomness call %s.%s is forbidden in deterministic test",
							owner,
							selector,
						),
					)
				}
				if strings.HasSuffix(importPath, "/uuid") &&
					strings.HasPrefix(selector, "New") {
					c.add(
						relative,
						line,
						fmt.Sprintf(
							"randomness call %s.%s is forbidden in deterministic test",
							owner,
							selector,
						),
					)
				}
				return true
			}
			if isRandomImport(importPath) {
				c.add(
					relative,
					line,
					fmt.Sprintf(
						"randomness call %s.%s is forbidden in deterministic production code",
						owner,
						selector,
					),
				)
			}
			if strings.HasSuffix(importPath, "/uuid") && strings.HasPrefix(selector, "New") {
				c.add(
					relative,
					line,
					fmt.Sprintf(
						"randomness call %s.%s is forbidden in deterministic production code",
						owner,
						selector,
					),
				)
			}
		}
		return true
	})

	if floatAllowed && hasFloat {
		c.usedFloat[relative] = struct{}{}
	}
	if panicAllowed && hasPanic {
		c.usedPanic[relative] = struct{}{}
	}
	return nil
}

func testingParameterNames(
	file *ast.File,
	imports map[string]string,
) map[string]struct{} {
	names := make(map[string]struct{})
	ast.Inspect(file, func(node ast.Node) bool {
		var parameters *ast.FieldList
		switch typed := node.(type) {
		case *ast.FuncDecl:
			parameters = typed.Type.Params
		case *ast.FuncLit:
			parameters = typed.Type.Params
		default:
			return true
		}
		if parameters == nil {
			return true
		}
		for _, parameter := range parameters.List {
			pointer, ok := parameter.Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			selector, ok := pointer.X.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "T" {
				continue
			}
			owner, ok := selector.X.(*ast.Ident)
			if !ok || imports[owner.Name] != "testing" {
				continue
			}
			for _, name := range parameter.Names {
				names[name.Name] = struct{}{}
			}
		}
		return true
	})
	return names
}

func importName(spec *ast.ImportSpec, importPath string) string {
	if spec.Name != nil {
		return spec.Name.Name
	}
	switch importPath {
	case "crypto/rand", "math/rand", "math/rand/v2":
		return "rand"
	default:
		return filepath.Base(importPath)
	}
}

func selectorCall(call *ast.CallExpr) (selector string, owner string, ok bool) {
	selected, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", "", false
	}
	identifier, ok := selected.X.(*ast.Ident)
	if !ok {
		return "", "", false
	}
	return selected.Sel.Name, identifier.Name, true
}

func isInfrastructureImport(importPath string) bool {
	exact := map[string]struct{}{
		"database/sql": {},
		"net/http":     {},
	}
	if _, ok := exact[importPath]; ok {
		return true
	}
	for _, prefix := range []string{
		"github.com/jackc/pgx",
		"github.com/nats-io/nats.go",
		"github.com/centrifugal/",
	} {
		if importPath == strings.TrimSuffix(prefix, "/") ||
			strings.HasPrefix(importPath, prefix) {
			return true
		}
	}
	return false
}

func isRandomImport(importPath string) bool {
	switch importPath {
	case "crypto/rand", "math/rand", "math/rand/v2":
		return true
	default:
		return false
	}
}

func isSeededRandomConstructor(selector string) bool {
	return strings.HasPrefix(selector, "New")
}

func isEnvironmentCall(selector string) bool {
	switch selector {
	case "Clearenv", "Environ", "ExpandEnv", "Getenv", "LookupEnv", "Setenv", "Unsetenv":
		return true
	default:
		return false
	}
}

func isInternalModelTest(path string) bool {
	return strings.HasPrefix(path, "internal/") || strings.HasPrefix(path, "testkit/")
}

func isPolicyTest(path string) bool {
	return isInternalModelTest(path) || strings.HasPrefix(path, "tests/")
}

func hasEntry(entries map[string]struct{}, path string) bool {
	_, ok := entries[path]
	return ok
}

func (c *checker) validateCompatibilityEntries(
	entries map[string]struct{},
	used map[string]struct{},
	message string,
) {
	for path := range entries {
		if _, ok := used[path]; ok {
			continue
		}
		c.add(path, 0, message)
	}
}

func (c *checker) add(path string, line int, message string) {
	c.problems = append(c.problems, problem{path: path, line: line, message: message})
}
