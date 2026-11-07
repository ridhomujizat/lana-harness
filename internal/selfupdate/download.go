package selfupdate

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const (
	// MaxDownloadSize caps the binary download at 200 MiB (design.md,
	// downloading).
	MaxDownloadSize int64 = 200 << 20
	// maxSumsSize caps the SHA256SUMS download.
	maxSumsSize int64 = 1 << 20
)

// ErrTooLarge is returned when a download passes its size cap.
var ErrTooLarge = errors.New("download is larger than the limit")

// Progress receives the bytes written so far and the expected total; total is
// 0 when unknown.
type Progress func(done, total int64)

// Download streams url into dest, replacing it, and stops at limit bytes. The
// progress callback runs after every chunk. A failed download removes dest.
func Download(ctx context.Context, client *http.Client, url, dest string, limit int64, progress Progress) (int64, error) {
	if limit <= 0 {
		limit = MaxDownloadSize
	}
	resp, err := get(ctx, client, url)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	total := resp.ContentLength
	if total < 0 {
		total = 0
	}
	if total > limit {
		_ = os.Remove(dest)
		return 0, fmt.Errorf("%w: %d bytes (limit %d)", ErrTooLarge, total, limit)
	}
	file, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	written, copyErr := copyWithProgress(ctx, file, io.LimitReader(resp.Body, limit+1), total, progress)
	if copyErr == nil && written > limit {
		copyErr = fmt.Errorf("%w: more than %d bytes", ErrTooLarge, limit)
	}
	if copyErr == nil {
		copyErr = file.Sync()
	}
	if closeErr := file.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = os.Remove(dest)
		return written, copyErr
	}
	return written, nil
}

// FetchSums downloads a SHA256SUMS file into memory (at most 1 MiB).
func FetchSums(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	resp, err := get(ctx, client, url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSumsSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSumsSize {
		return nil, fmt.Errorf("%w: %s", ErrTooLarge, SumsAsset)
	}
	return data, nil
}

func get(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "lana-selfupdate")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	return resp, nil
}

func copyWithProgress(ctx context.Context, dst io.Writer, src io.Reader, total int64, progress Progress) (int64, error) {
	buf := make([]byte, 64<<10)
	var written int64
	if progress != nil {
		progress(0, total)
	}
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			w, err := dst.Write(buf[:n])
			written += int64(w)
			if err != nil {
				return written, err
			}
			if progress != nil {
				progress(written, total)
			}
		}
		if errors.Is(readErr, io.EOF) {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}

// ParseSums reads "<hex>  <name>" and "<hex> *<name>" lines into name -> hex.
// Blank lines and lines that are not a checksum entry are skipped.
func ParseSums(data []byte) map[string]string {
	out := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sum, name, ok := strings.Cut(line, " ")
		if !ok || len(sum) != sha256.Size*2 {
			continue
		}
		if _, err := hex.DecodeString(sum); err != nil {
			continue
		}
		name = strings.TrimLeft(name, " ")
		name = strings.TrimPrefix(name, "*")
		if name == "" {
			continue
		}
		out[name] = strings.ToLower(sum)
	}
	return out
}

// FileSHA256 returns the hex SHA-256 of a file.
func FileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// VerifyFile checks path against the SHA256SUMS entry for name.
func VerifyFile(path, name string, sums []byte) error {
	want, ok := ParseSums(sums)[name]
	if !ok {
		return fmt.Errorf("%s has no entry for %s", SumsAsset, name)
	}
	got, err := FileSHA256(path)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", name, got, want)
	}
	return nil
}
