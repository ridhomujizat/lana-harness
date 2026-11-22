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

// codesignCommand runs /usr/bin/codesign with args. Tests replace it.
var codesignCommand = func(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "/usr/bin/codesign", args...).CombinedOutput()
}

// runCodesign runs codesign with its own resealTimeout.
func runCodesign(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), resealTimeout)
	defer cancel()
	return codesignCommand(ctx, args...)
}

// parseSignature pulls the bundle Identifier and the first signing Authority
// out of `codesign -dvv` output. authority is empty for an ad-hoc signature.
func parseSignature(out []byte) (identifier, authority string) {
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if identifier == "" && strings.HasPrefix(line, "Identifier=") {
			identifier = strings.TrimPrefix(line, "Identifier=")
		}
		if authority == "" && strings.HasPrefix(line, "Authority=") {
			authority = strings.TrimPrefix(line, "Authority=")
		}
	}
	return identifier, authority
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

// ResealAppBundle signs the app bundle around exePath again after its lana
// binary was replaced, so `codesign --verify` keeps passing (spec #52). When
// the bundle carries a real signing identity, the replaced nested binary and
// the bundle are signed with that same identity and the bundle's identifier;
// keeping the identity is what lets macOS TCC keep its privacy grants across
// updates, because TCC keys an ad-hoc signed program on its cdhash and a fresh
// ad-hoc signature therefore looks like a new program. A bundle without an
// identity, or one whose identity signing fails, is signed ad-hoc again. A
// binary outside a bundle, or another OS, is a no-op. codesign writes the
// signed files through a rename, so a running menu bar app keeps its mapped
// executable.
func ResealAppBundle(exePath string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	bundle, ok := AppBundle(exePath)
	if !ok {
		return nil
	}
	return resealBundle(exePath, bundle)
}

// resealBundle keeps the bundle's signing identity when it has one and falls
// back to an ad-hoc signature when it does not or when signing with the
// identity fails.
func resealBundle(exePath, bundle string) error {
	identifier, authority := bundleSignature(bundle)
	if authority == "" {
		return signAdHoc(bundle)
	}
	daemonID := ""
	if identifier != "" {
		daemonID = identifier + ".daemon"
	}
	if err := signIdentity(exePath, authority, daemonID); err != nil {
		slog.Warn("update: sign with the app identity", "identity", authority, "error", err)
		return resealAdHoc(exePath, bundle)
	}
	if err := signIdentity(bundle, authority, ""); err != nil {
		slog.Warn("update: sign with the app identity", "identity", authority, "error", err)
		return resealAdHoc(exePath, bundle)
	}
	return nil
}

// bundleSignature reads the bundle's current signature. A failed read or a
// missing Authority line means the bundle is ad-hoc signed.
func bundleSignature(bundle string) (identifier, authority string) {
	out, err := runCodesign("-dvv", bundle)
	if err != nil {
		return "", ""
	}
	return parseSignature(out)
}

// signIdentity re-signs path with identity, naming it identifier when that is
// not empty.
func signIdentity(path, identity, identifier string) error {
	args := []string{"--force", "--sign", identity}
	if identifier != "" {
		args = append(args, "--identifier", identifier)
	}
	args = append(args, path)
	out, err := runCodesign(args...)
	if err != nil {
		return fmt.Errorf("codesign %s: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// signAdHoc signs path with an ad-hoc signature.
func signAdHoc(path string) error {
	out, err := runCodesign("--force", "--sign", "-", path)
	if err != nil {
		return fmt.Errorf("codesign %s: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// resealAdHoc is the fallback when the identity is unavailable: sign the
// nested binary and the bundle ad-hoc so codesign --verify --strict passes.
func resealAdHoc(exePath, bundle string) error {
	if err := signAdHoc(exePath); err != nil {
		return err
	}
	return signAdHoc(bundle)
}

// resealAfterReplace reseals best effort: the new binary runs either way, so
// a failure is only a warning.
func resealAfterReplace(exePath string) {
	if err := ResealAppBundle(exePath); err != nil {
		slog.Warn("update: sign the app bundle again", "error", err)
	}
}
