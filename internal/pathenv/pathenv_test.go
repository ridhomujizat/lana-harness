package pathenv

import (
	"os"
	"strings"
	"testing"
)

func pathOf(env []string) (string, int) {
	value, count := "", 0
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			value = strings.TrimPrefix(kv, "PATH=")
			count++
		}
	}
	return value, count
}

func TestEnvNoDirsInherits(t *testing.T) {
	if env := Env(nil); env != nil {
		t.Fatalf("env=%v, want nil (inherit)", env)
	}
	if env := Env([]string{""}); env != nil {
		t.Fatalf("env=%v, want nil for blank dirs", env)
	}
}

func TestEnvPrependsDirs(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("PATHENV_TEST", "kept")
	env := Env([]string{"/a/bin", "", "/b/bin"})
	path, count := pathOf(env)
	if count != 1 || path != "/a/bin:/b/bin:/usr/bin:/bin" {
		t.Fatalf("PATH=%q count=%d", path, count)
	}
	found := false
	for _, kv := range env {
		if kv == "PATHENV_TEST=kept" {
			found = true
		}
	}
	if !found {
		t.Fatal("other env vars were not kept")
	}
}

func TestEnvUnsetPath(t *testing.T) {
	t.Setenv("PATH", "")
	_ = os.Unsetenv("PATH")
	path, count := pathOf(Env([]string{"/a/bin"}))
	if count != 1 || !strings.HasPrefix(path, "/a/bin") {
		t.Fatalf("PATH=%q count=%d", path, count)
	}
}
