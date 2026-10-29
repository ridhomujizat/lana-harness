package archtest

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"testing"
)

const modulePath = "github.com/ridhomujizat/lana-harness"

// checkRules memeriksa import langsung per package dalam module.
// Input: path package relatif terhadap module ("" untuk root) ke daftar import.
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
				violations = append(violations, fmt.Sprintf("package %s tidak boleh meng-import %s", display(pkg), display(rel)))
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
	if from == "cmd/lana" || strings.HasPrefix(from, "examples/") {
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
	if from == "config" || from == "internal" || strings.HasPrefix(from, "internal/") {
		return isInternal(to)
	}
	// Package implementasi dan library hanya boleh core atau internal.
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
	for _, prefix := range []string{"agent", "providers/", "tools", "store/", "observability/"} {
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
		t.Fatalf("aturan import dilanggar:\n%s", strings.Join(violations, "\n"))
	}
}

func TestCoreTransitive(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", modulePath+"/core").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(line, modulePath) && line != modulePath+"/core" {
			t.Fatalf("core meng-import package module lain: %s", line)
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
			t.Fatalf("pesan pelanggaran tidak memuat %q: %s", want, joined)
		}
	}
}
