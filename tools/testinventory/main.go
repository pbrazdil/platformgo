// Command testinventory discovers Rust test functions in the pinned source
// trees and writes the initial clean-room port ledger.
package main

import (
	"bufio"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	platformRevision = "50141367492be46ebf5623f6191a14b94af2f2bd"
	nautilusRevision = "116c9b5159ebeb6b578b737d72298cac8d723723"
)

var functionPattern = regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(?:const\s+)?(?:async\s+)?fn\s+([A-Za-z_][A-Za-z0-9_]*)`)

type source struct {
	repo     string
	revision string
	root     string
	scopes   []string
}

type test struct {
	repo     string
	revision string
	file     string
	name     string
	line     int
	category string
}

type sourceTest struct {
	name string
	line int
}

func main() {
	var platformRoot string
	var nautilusRoot string
	var output string

	flag.StringVar(&platformRoot, "platform", "", "path to the pinned platform checkout")
	flag.StringVar(&nautilusRoot, "nautilus", "", "path to the pinned NautilusTrader checkout")
	flag.StringVar(&output, "out", "", "path to test-port-map.csv")
	flag.Parse()

	if platformRoot == "" || nautilusRoot == "" || output == "" {
		fail(errors.New("-platform, -nautilus, and -out are required"))
	}

	sources := []source{
		{
			repo:     "platform",
			revision: platformRevision,
			root:     platformRoot,
			scopes: []string{
				"apps/nautilus/tests",
				"apps/app/tests",
			},
		},
		{
			repo:     "nautilus",
			revision: nautilusRevision,
			root:     nautilusRoot,
			scopes: []string{
				"crates/model/src",
			},
		},
	}

	var tests []test
	for _, src := range sources {
		if err := verifyRevision(src); err != nil {
			fail(err)
		}
		found, err := discover(src)
		if err != nil {
			fail(err)
		}
		tests = append(tests, found...)
	}

	slices.SortFunc(tests, func(a, b test) int {
		if c := strings.Compare(a.repo, b.repo); c != 0 {
			return c
		}
		if c := strings.Compare(a.file, b.file); c != 0 {
			return c
		}
		if c := strings.Compare(a.name, b.name); c != 0 {
			return c
		}
		return a.line - b.line
	})

	if err := ensureSafeToWrite(output); err != nil {
		fail(err)
	}
	if err := writeCSV(output, tests); err != nil {
		fail(err)
	}

	fmt.Printf("wrote %d source tests to %s\n", len(tests), output)
}

func verifyRevision(src source) error {
	head, err := os.ReadFile(filepath.Join(src.root, ".git", "HEAD"))
	if err == nil && strings.HasPrefix(string(head), src.revision) {
		return nil
	}

	gitDir, err := os.Open(filepath.Join(src.root, ".git"))
	if err == nil {
		_ = gitDir.Close()
	}

	// Worktree HEAD files are commonly symbolic or stored through a gitdir
	// indirection. Resolve without invoking source build tooling.
	resolved, err := readGitHead(src.root)
	if err != nil {
		return fmt.Errorf("%s: resolve pinned revision: %w", src.repo, err)
	}
	if resolved != src.revision {
		return fmt.Errorf("%s checkout is %s, want %s", src.repo, resolved, src.revision)
	}
	return nil
}

func readGitHead(root string) (string, error) {
	gitPath := filepath.Join(root, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return "", err
	}

	gitDir := gitPath
	if !info.IsDir() {
		data, err := os.ReadFile(gitPath)
		if err != nil {
			return "", err
		}
		const prefix = "gitdir: "
		line := strings.TrimSpace(string(data))
		if !strings.HasPrefix(line, prefix) {
			return "", fmt.Errorf("unsupported .git file")
		}
		gitDir = strings.TrimPrefix(line, prefix)
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(root, gitDir)
		}
	}

	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(head))
	const refPrefix = "ref: "
	if !strings.HasPrefix(value, refPrefix) {
		return value, nil
	}
	ref := strings.TrimPrefix(value, refPrefix)
	data, err := os.ReadFile(filepath.Join(gitDir, filepath.FromSlash(ref)))
	if err == nil {
		return strings.TrimSpace(string(data)), nil
	}

	packed, packedErr := os.Open(filepath.Join(gitDir, "packed-refs"))
	if packedErr != nil {
		return "", err
	}
	defer packed.Close()

	scanner := bufio.NewScanner(packed)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[1] == ref {
			return fields[0], nil
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return "", scanErr
	}
	return "", fmt.Errorf("ref %s not found", ref)
}

func discover(src source) ([]test, error) {
	var tests []test
	for _, scope := range src.scopes {
		scopeRoot := filepath.Join(src.root, filepath.FromSlash(scope))
		err := filepath.WalkDir(scopeRoot, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(path) != ".rs" {
				return nil
			}

			relative, err := filepath.Rel(src.root, path)
			if err != nil {
				return err
			}
			found, err := testsInFile(path)
			if err != nil {
				return err
			}
			for _, sourceTest := range found {
				file := filepath.ToSlash(relative)
				tests = append(tests, test{
					repo:     src.repo,
					revision: src.revision,
					file:     file,
					name:     sourceTest.name,
					line:     sourceTest.line,
					category: classify(file),
				})
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", scopeRoot, err)
		}
	}
	return tests, nil
}

func testsInFile(path string) ([]sourceTest, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var tests []sourceTest
	var attributes []string
	attributeDepth := 0
	lineNumber := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if attributeDepth > 0 {
			attributes = append(attributes, line)
			attributeDepth += strings.Count(line, "[") - strings.Count(line, "]")
			continue
		}
		switch {
		case strings.HasPrefix(line, "#["):
			attributes = append(attributes, line)
			attributeDepth = strings.Count(line, "[") - strings.Count(line, "]")
		case line == "" || strings.HasPrefix(line, "///") || strings.HasPrefix(line, "//"):
			continue
		default:
			match := functionPattern.FindStringSubmatch(line)
			if len(match) == 2 && isTest(attributes) {
				tests = append(tests, sourceTest{name: match[1], line: lineNumber})
			}
			attributes = nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return tests, nil
}

func isTest(attributes []string) bool {
	for _, attribute := range attributes {
		normalized := strings.ReplaceAll(attribute, " ", "")
		if strings.HasPrefix(normalized, "#[test") ||
			strings.HasPrefix(normalized, "#[tokio::test") ||
			strings.HasPrefix(normalized, "#[rstest") {
			return true
		}
	}
	return false
}

func classify(path string) string {
	switch {
	case strings.Contains(path, "/messaging/"):
		return "integration-messaging"
	case strings.Contains(path, "/realtime/"):
		return "contract-realtime"
	case strings.Contains(path, "/recovery/"):
		return "integration-postgres"
	case strings.Contains(path, "/live/catalog/"):
		return "adapter-hyperliquid"
	case strings.Contains(path, "/tests/it/"):
		return "integration-postgres"
	case strings.HasPrefix(path, "crates/model/src/"):
		return "unit"
	default:
		return "model"
	}
}

func ensureSafeToWrite(path string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	rows, err := reader.ReadAll()
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	statusColumn := slices.Index(rows[0], "status")
	if statusColumn < 0 {
		return fmt.Errorf("%s has no status column", path)
	}
	for _, row := range rows[1:] {
		if len(row) <= statusColumn {
			return fmt.Errorf("%s contains a malformed row", path)
		}
		if row[statusColumn] != "discovered" {
			return fmt.Errorf("%s contains status %q; refusing to overwrite porting work", path, row[statusColumn])
		}
	}
	return nil
}

func writeCSV(path string, tests []test) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}

	writer := csv.NewWriter(file)
	write := func(row []string) error {
		if err := writer.Write(row); err != nil {
			return err
		}
		return nil
	}

	if err := write([]string{
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
	}); err != nil {
		_ = file.Close()
		return err
	}
	for _, test := range tests {
		if err := write([]string{
			test.repo,
			test.revision,
			test.file,
			test.name,
			strconv.Itoa(test.line),
			"",
			"",
			test.category,
			"discovered",
			"",
		}); err != nil {
			_ = file.Close()
			return err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
