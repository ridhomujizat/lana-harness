package toolbox

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testService builds a Service over a fresh temp dir (never the real HOME) with
// a discarded logger and closes it at the end of the test.
func testService(t *testing.T, opts ...Option) *Service {
	t.Helper()
	all := append([]Option{WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))}, opts...)
	svc := New(t.TempDir(), all...)
	t.Cleanup(svc.Close)
	return svc
}

// writeScript writes an executable script into the package bin directory.
func writeScript(t *testing.T, store *Store, id, name, script string) {
	t.Helper()
	if err := os.MkdirAll(store.BinDir(id), 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.BinDir(id), name), []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
}

// addManifest stores a manifest and fails the test when it is invalid.
func addManifest(t *testing.T, svc *Service, text string) Package {
	t.Helper()
	pkg, issues, err := svc.Add(context.Background(), text)
	if err != nil {
		t.Fatalf("add manifest: %v", err)
	}
	if len(issues) > 0 {
		t.Fatalf("add manifest issues: %+v", issues)
	}
	return pkg
}

// waitJob polls until a job leaves the running state and returns its snapshot.
func waitJob(t *testing.T, svc *Service, id, jobID string) Job {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		for _, job := range svc.Jobs(id) {
			if job.ID == jobID && job.Status != JobRunning {
				return job
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s did not finish", jobID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// fakeBinary is the version script the detection and run tests install.
const fakeBinary = `#!/bin/sh
echo "faketool 0.2.2"
`
