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

var functionPattern = regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(?:const\s+)?(?:async\s+)?fn\s+([A-Za-z_][A-Za-z0-9_]*)`)
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type source struct {
	repo          string
	revision      string
	root          string
	scopes        []string
	expectedCount int
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
	var sourcePolicyPath string

	flag.StringVar(&platformRoot, "platform", "", "path to the pinned platform checkout")
	flag.StringVar(&nautilusRoot, "nautilus", "", "path to the pinned NautilusTrader checkout")
	flag.StringVar(&output, "out", "", "path to test-port-map.csv")
	flag.StringVar(
		&sourcePolicyPath,
		"source-policy",
		"ports/SOURCE_REVISIONS.md",
		"path to the canonical source-revision policy",
	)
	flag.Parse()

	if platformRoot == "" || nautilusRoot == "" || output == "" {
		fail(errors.New("-platform, -nautilus, and -out are required"))
	}

	sources, err := loadSourcePolicy(sourcePolicyPath)
	if err != nil {
		fail(err)
	}
	for index := range sources {
		switch sources[index].repo {
		case "platform":
			sources[index].root = platformRoot
		case "nautilus":
			sources[index].root = nautilusRoot
		default:
			fail(fmt.Errorf("unsupported source alias %q", sources[index].repo))
		}
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
		if len(found) != src.expectedCount {
			fail(fmt.Errorf(
				"%s inventory has %d tests, want %d from %s",
				src.repo,
				len(found),
				src.expectedCount,
				sourcePolicyPath,
			))
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

func loadSourcePolicy(path string) ([]source, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read source policy: %w", err)
	}
	values := make(map[string]string)
	for line := range strings.Lines(string(data)) {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || key == "" || value == "" {
			continue
		}
		if matched, _ := regexp.MatchString(`^[A-Z][A-Z0-9_]*$`, key); matched {
			values[key] = value
		}
	}

	definitions := []struct {
		alias       string
		prefix      string
		revisionKey string
	}{
		{alias: "platform", prefix: "PLATFORM", revisionKey: "PLATFORM_SOURCE_COMMIT"},
		{alias: "nautilus", prefix: "NAUTILUS", revisionKey: "NAUTILUS_SOURCE_REVISION"},
	}
	sources := make([]source, 0, len(definitions))
	for _, definition := range definitions {
		repositoryKey := definition.prefix + "_SOURCE_REPOSITORY"
		rootsKey := definition.prefix + "_SOURCE_ROOTS"
		countKey := definition.prefix + "_SOURCE_TEST_COUNT"
		for _, key := range []string{
			repositoryKey,
			definition.revisionKey,
			rootsKey,
			countKey,
		} {
			if values[key] == "" {
				return nil, fmt.Errorf("%s is missing %s", path, key)
			}
		}
		revision := values[definition.revisionKey]
		if !commitPattern.MatchString(revision) {
			return nil, fmt.Errorf("%s must contain a 40-character Git commit", definition.revisionKey)
		}
		scopes := strings.Split(values[rootsKey], ",")
		for _, scope := range scopes {
			relative := filepath.Clean(filepath.FromSlash(scope))
			if scope == "" || filepath.IsAbs(relative) ||
				relative == ".." || strings.HasPrefix(filepath.ToSlash(relative), "../") {
				return nil, fmt.Errorf("%s contains invalid source root %q", rootsKey, scope)
			}
		}
		expectedCount, parseErr := strconv.Atoi(values[countKey])
		if parseErr != nil || expectedCount <= 0 {
			return nil, fmt.Errorf("%s must be a positive integer", countKey)
		}
		sources = append(sources, source{
			repo:          definition.alias,
			revision:      revision,
			scopes:        scopes,
			expectedCount: expectedCount,
		})
	}
	return sources, nil
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
		data, readErr := os.ReadFile(gitPath)
		if readErr != nil {
			return "", readErr
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
	defer func() {
		_ = packed.Close()
	}()

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
	defer func() {
		_ = file.Close()
	}()

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
	defer func() {
		_ = file.Close()
	}()

	reader := csv.NewReader(file)
	rows, err := reader.ReadAll()
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	statusColumn := slices.Index(rows[0], "port_status")
	if statusColumn < 0 {
		return fmt.Errorf("%s has no port_status column", path)
	}
	for _, row := range rows[1:] {
		if len(row) <= statusColumn {
			return fmt.Errorf("%s contains a malformed row", path)
		}
		if row[statusColumn] != "discovered" {
			return fmt.Errorf("%s contains port_status %q; refusing to overwrite porting work", path, row[statusColumn])
		}
	}
	return nil
}

func writeCSV(path string, tests []test) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
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
		"port_status",
		"review_status",
		"wiring_status",
		"evidence",
		"milestone",
		"port_owner",
		"implementation_owner",
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
			"unreviewed",
			"placeholder",
			"",
			"",
			"",
			"",
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
