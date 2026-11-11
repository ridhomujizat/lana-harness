package selfupdate

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// resealTimeout bounds one codesign run.
const resealTimeout = 30 * time.Second

// resealCommand runs codesign. Tests replace it.
var resealCommand = func(ctx context.Context, bundle string) ([]byte, error) {
	return exec.CommandContext(ctx, "/usr/bin/codesign", "--force", "--sign", "-", bundle).CombinedOutput()
}

// AppBundle returns the macOS app bundle that holds exePath directly in its
// Contents/MacOS folder, such as /Applications/Lana.app for
// /Applications/Lana.app/Contents/MacOS/lana (spec #52). ok is false for a
// binary outside a bundle.
func AppBundle(exePath string) (string, bool) {
	clean := filepath.Clean(exePath)
	macos := filepath.Dir(clean)
	contents := filepath.Dir(macos)
	bundle := filepath.Dir(contents)
	if filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" {
		return "", false
	}
	if !strings.HasSuffix(bundle, ".app") || filepath.Base(bundle) == ".app" {
		return "", false
	}
	return bundle, true
}

// ResealAppBundle signs the app bundle around exePath again with an ad-hoc
// signature after its lana binary was replaced, so `codesign --verify` keeps
// passing (spec #52). A binary outside a bundle, or another OS, is a no-op.
// codesign writes the signed files through a rename, so a running menu bar
// app keeps its mapped executable.
func ResealAppBundle(exePath string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	bundle, ok := AppBundle(exePath)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), resealTimeout)
	defer cancel()
	out, err := resealCommand(ctx, bundle)
	if err != nil {
		return fmt.Errorf("codesign %s: %w: %s", bundle, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// resealAfterReplace reseals best effort: the new binary runs either way, so
// a failure is only a warning.
func resealAfterReplace(exePath string) {
	if err := ResealAppBundle(exePath); err != nil {
		slog.Warn("update: sign the app bundle again", "error", err)
	}
}
