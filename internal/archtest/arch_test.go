package archtest

import (
	"fmt"
	"os/exec"
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
	for _, prefix := range []string{"agent", "providers/", "tools", "store/", "observability/", "router"} {
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
		"router":             {modulePath + "/core", modulePath + "/internal/models"},
		"router/openaiproxy": {modulePath + "/router", modulePath + "/core"},
		"providers/openai":   {modulePath + "/router", modulePath + "/core"},
		"":                   {modulePath + "/router"},
		"examples/router":    {modulePath + "/router", modulePath + "/providers/mock"},
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
