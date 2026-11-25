package archtest

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const modulePath = "github.com/ridhomujizat/lana-harness"

// checkRules checks direct imports for each package in the module.
// Input: package paths relative to the module ("" for root) mapped to their import lists.
func checkRules(imports map[string][]string) []string {
	var violations []string
	names := make([]string, 0, len(imports))
	for name := range imports {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, pkg := range names {
		for _, imp := range imports[pkg] {
			if !strings.HasPrefix(imp, modulePath) {
				continue
			}
			rel := strings.TrimPrefix(imp, modulePath)
			rel = strings.TrimPrefix(rel, "/")
			if rel == "" {
				rel = "."
			}
			if !allowed(pkg, rel) {
				violations = append(violations, fmt.Sprintf("package %s must not import %s", display(pkg), display(rel)))
			}
		}
	}
	return violations
}

func display(pkg string) string {
	if pkg == "" || pkg == "." {
		return modulePath
	}
	return pkg
}

func allowed(from, to string) bool {
	// cmd/lana-eval is a developer command like cmd/lana: it drives the public
	// library runtime directly.
	if from == "cmd/lana" || from == "cmd/lana-eval" || strings.HasPrefix(from, "examples/") {
		return true
	}
	if from == "" {
		for _, forbidden := range []string{"channels", "web", "protocol"} {
			if to == forbidden || strings.HasPrefix(to, forbidden+"/") {
				return false
			}
		}
		return true
	}
	if from == "core" {
		return false
	}
	// mcp_manage validates its candidate with config.Check (spec #12).
	if from == "tools" && to == "config" {
		return true
	}
	if from == "config" || from == "internal" || strings.HasPrefix(from, "internal/") {
		return isInternal(to)
	}
	// The router is an embeddable library (ADR 0019): router imports only
	// core and internal; router/* may also import router.
	if from == "router" {
		return isInternal(to)
	}
	if strings.HasPrefix(from, "router/") {
		return to == "router" || isInternal(to)
	}
	// Provider adapters register their kinds with the router.
	if strings.HasPrefix(from, "providers/") && to == "router" {
		return true
	}
	// Implementation and library packages may only import core or internal.
	if !isInternal(to) {
		return false
	}
	if isLibrary(from) {
		for _, forbidden := range []string{"channels", "web", "protocol"} {
			if to == forbidden || strings.HasPrefix(to, forbidden+"/") {
				return false
			}
		}
	}
	return true
}

func isInternal(pkg string) bool {
	return pkg == "core" || pkg == "internal" || strings.HasPrefix(pkg, "internal/")
}

func isLibrary(pkg string) bool {
	if pkg == "" || pkg == "." || pkg == "lana" {
		return true
	}
	// runtime/* holds external agent runtimes such as runtime/claudecode (ADR 0018).
	for _, prefix := range []string{"agent", "providers/", "tools", "store/", "observability/", "router", "runtime/"} {
		if pkg == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(pkg, prefix) {
			return true
		}
	}
	return false
}

func moduleImports(t *testing.T) map[string][]string {
	t.Helper()
	out, err := exec.Command("go", "list", "-f", `{{.ImportPath}} {{join .Imports " "}}`, modulePath+"/...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	result := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		pkg := strings.TrimPrefix(fields[0], modulePath+"/")
		if fields[0] == modulePath {
			pkg = ""
		}
		var imps []string
		for _, imp := range fields[1:] {
			if strings.HasPrefix(imp, modulePath) {
				imps = append(imps, imp)
			}
		}
		result[pkg] = imps
	}
	return result
}

func TestImportRules(t *testing.T) {
	violations := checkRules(moduleImports(t))
	if len(violations) > 0 {
		t.Fatalf("import rules violated:\n%s", strings.Join(violations, "\n"))
	}
}

func TestCoreTransitive(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", modulePath+"/core").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(line, modulePath) && line != modulePath+"/core" {
			t.Fatalf("core imports a package from another module: %s", line)
		}
	}
}

func TestRuleViolationMessage(t *testing.T) {
	got := checkRules(map[string][]string{
		"core": {modulePath + "/config"},
		"lana": {modulePath + "/channels/cli"},
	})
	joined := strings.Join(got, "\n")
	for _, want := range []string{"core", "config", "lana", "channels/cli"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("violation message does not contain %q: %s", want, joined)
		}
	}
}

// ADR 0019: router imports only core and internal; router/* may import
// router; providers may import router; nothing else may.
func TestRouterRules(t *testing.T) {
	ok := checkRules(map[string][]string{
		"router":                {modulePath + "/core", modulePath + "/internal/models"},
		"router/openaiproxy":    {modulePath + "/router", modulePath + "/core"},
		"providers/openai":      {modulePath + "/router", modulePath + "/core"},
		"providers/commandcode": {modulePath + "/router", modulePath + "/core"},
		"":                      {modulePath + "/router"},
		"examples/router":       {modulePath + "/router", modulePath + "/providers/mock"},
	})
	if len(ok) > 0 {
		t.Fatalf("unexpected violations: %v", ok)
	}
	bad := checkRules(map[string][]string{
		"router":             {modulePath + "/agent", modulePath + "/web", modulePath + "/providers/openai"},
		"router/openaiproxy": {modulePath + "/web", modulePath + "/store/sqlite"},
		"agent":              {modulePath + "/router"},
		"core":               {modulePath + "/router"},
		"config":             {modulePath + "/router"},
	})
	if len(bad) != 8 {
		t.Fatalf("violations = %d, want 8:\n%s", len(bad), strings.Join(bad, "\n"))
	}
}

// runtime/* packages are libraries: they may import core and internal only.
func TestRuntimeRules(t *testing.T) {
	ok := checkRules(map[string][]string{
		"runtime/claudecode": {modulePath + "/core", modulePath + "/internal/pattern"},
	})
	if len(ok) > 0 {
		t.Fatalf("allowed imports rejected: %v", ok)
	}
	for _, imp := range []string{"agent", "config", "channels/telegram", "web", "protocol", "tools"} {
		got := checkRules(map[string][]string{"runtime/claudecode": {modulePath + "/" + imp}})
		if len(got) != 1 {
			t.Fatalf("runtime/claudecode importing %s: violations = %v", imp, got)
		}
	}
	if !isLibrary("runtime/claudecode") {
		t.Fatal("runtime/claudecode is not a library package")
	}
	// ADR 0020: the loopback MCP host is a runtime library too: core and
	// internal only (the MCP SDK is an external module); the agent package
	// reaches it through core.RuntimeToolHost, and the root package wires it.
	ok = checkRules(map[string][]string{
		"runtime/mcphost": {modulePath + "/core"},
		"":                {modulePath + "/runtime/mcphost"},
	})
	if len(ok) > 0 {
		t.Fatalf("allowed imports rejected: %v", ok)
	}
	for _, imp := range []string{"agent", "runtime/claudecode", "router", "web", "tools"} {
		if got := checkRules(map[string][]string{"runtime/mcphost": {modulePath + "/" + imp}}); len(got) != 1 {
			t.Fatalf("runtime/mcphost importing %s: violations = %v", imp, got)
		}
	}
	if got := checkRules(map[string][]string{"agent": {modulePath + "/runtime/mcphost"}}); len(got) != 1 {
		t.Fatalf("agent importing runtime/mcphost: violations = %v", got)
	}
	// ADR 0022: the Antigravity runtime is a library too: core and internal
	// only. No runtime imports another runtime.
	ok = checkRules(map[string][]string{
		"runtime/antigravity": {modulePath + "/core", modulePath + "/internal/pattern"},
	})
	if len(ok) > 0 {
		t.Fatalf("allowed imports rejected: %v", ok)
	}
	for _, imp := range []string{"agent", "config", "channels/telegram", "web", "protocol", "tools", "runtime/claudecode"} {
		if got := checkRules(map[string][]string{"runtime/antigravity": {modulePath + "/" + imp}}); len(got) != 1 {
			t.Fatalf("runtime/antigravity importing %s: violations = %v", imp, got)
		}
	}
	if !isLibrary("runtime/antigravity") {
		t.Fatal("runtime/antigravity is not a library package")
	}
}

// AC-AGY-005: no non-test Go file of the runtime packages or the root package
// mentions an agy credential term or a Google API URL. lana learns about an
// account only from agy's own output.
func TestNoCredentialStrings(t *testing.T) {
	root := repoRoot(t)
	terms := []string{"antigravity-oauth-token", "acp_token", "find-generic-password", "googleapis.com"}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "node_modules", "dist":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		// Only the runtime packages and the root package (AC-AGY-005).
		if strings.Contains(rel, string(filepath.Separator)) && !strings.HasPrefix(rel, "runtime"+string(filepath.Separator)) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, term := range terms {
			if bytes.Contains(data, []byte(term)) {
				t.Errorf("%s mentions %q", filepath.ToSlash(rel), term)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// repoRoot is the module root: two levels above internal/archtest.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}
