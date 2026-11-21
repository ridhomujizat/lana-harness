package gitinfo

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseUnifiedDiffHunks(t *testing.T) {
	text := `diff --git a/f.txt b/f.txt
index 1111111..2222222 100644
--- a/f.txt
+++ b/f.txt
@@ -1,3 +1,4 @@
 ctx1
-del2
+add2
 ctx3
+add4
@@ -10,2 +11,2 @@
 ctx10
-old11
+new11
`
	hunks, binary, truncated := ParseUnifiedDiff(text, 0)
	if binary || truncated {
		t.Fatalf("binary = %v, truncated = %v, want false/false", binary, truncated)
	}
	if len(hunks) != 2 {
		t.Fatalf("hunks = %d, want 2", len(hunks))
	}
	if hunks[0].OldStart != 1 || hunks[0].NewStart != 1 {
		t.Errorf("hunk 0 starts = %d/%d, want 1/1", hunks[0].OldStart, hunks[0].NewStart)
	}
	if hunks[1].OldStart != 10 || hunks[1].NewStart != 11 {
		t.Errorf("hunk 1 starts = %d/%d, want 10/11", hunks[1].OldStart, hunks[1].NewStart)
	}
	want := []Line{
		{Kind: "ctx", Old: 1, New: 1, Text: "ctx1"},
		{Kind: "del", Old: 2, Text: "del2"},
		{Kind: "add", New: 2, Text: "add2"},
		{Kind: "ctx", Old: 3, New: 3, Text: "ctx3"},
		{Kind: "add", New: 4, Text: "add4"},
	}
	if !reflect.DeepEqual(hunks[0].Lines, want) {
		t.Errorf("hunk 0 lines = %+v, want %+v", hunks[0].Lines, want)
	}
}

func TestParseUnifiedDiffNoNewline(t *testing.T) {
	text := `--- a/f.txt
+++ b/f.txt
@@ -1 +1 @@
-old
+new
\ No newline at end of file`
	hunks, binary, truncated := ParseUnifiedDiff(text, 0)
	if binary || truncated {
		t.Fatalf("binary = %v, truncated = %v, want false/false", binary, truncated)
	}
	if len(hunks) != 1 || len(hunks[0].Lines) != 2 {
		t.Fatalf("hunks = %+v", hunks)
	}
	if hunks[0].Lines[1].NoNewline != true || hunks[0].Lines[1].Kind != "add" {
		t.Errorf("last line = %+v, want add with no_newline", hunks[0].Lines[1])
	}
	if hunks[0].Lines[0].NoNewline {
		t.Errorf("del line has no_newline set: %+v", hunks[0].Lines[0])
	}
}

func TestParseUnifiedDiffBinary(t *testing.T) {
	text := `diff --git a/bin.dat b/bin.dat
index 0000000..badc806
Binary files a/bin.dat and b/bin.dat differ
`
	hunks, binary, truncated := ParseUnifiedDiff(text, 0)
	if !binary {
		t.Errorf("binary = false, want true")
	}
	if truncated {
		t.Errorf("truncated = true, want false")
	}
	if hunks != nil {
		t.Errorf("hunks = %+v, want nil", hunks)
	}
}

func TestParseUnifiedDiffTruncated(t *testing.T) {
	text := "@@ -1,10 +1,10 @@\n" + strings.Repeat("+line\n", 10)
	hunks, _, truncated := ParseUnifiedDiff(text, 5)
	if !truncated {
		t.Fatalf("truncated = false, want true")
	}
	if len(hunks) != 1 || len(hunks[0].Lines) != 5 {
		t.Fatalf("hunks = %+v, want one hunk of 5 lines", hunks)
	}

	exact := "@@ -1,3 +1,3 @@\n+a\n+b\n+c"
	hunks, _, truncated = ParseUnifiedDiff(exact, 3)
	if truncated {
		t.Errorf("truncated = true for a diff of exactly the limit")
	}
	if len(hunks) != 1 || len(hunks[0].Lines) != 3 {
		t.Errorf("hunks = %+v, want one hunk of 3 lines", hunks)
	}
}

func TestDiffModified(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "f.txt", "one\ntwo\nthree\n")
	runGit(t, repo, "add", "f.txt")
	runGit(t, repo, "commit", "-qm", "add")
	writeFile(t, repo, "f.txt", "one\nTWO\nthree\n")

	r := NewRunner(allowPolicy(root))
	fd, err := r.Diff(context.Background(), repo, "f.txt", ScopeUncommitted, "")
	if err != nil {
		t.Fatal(err)
	}
	if fd.Path != "f.txt" || fd.Status != statusModified {
		t.Errorf("path/status = %q/%q, want f.txt/M", fd.Path, fd.Status)
	}
	if fd.Binary || fd.Truncated {
		t.Errorf("binary/truncated = %v/%v, want false/false", fd.Binary, fd.Truncated)
	}
	if len(fd.Hunks) != 1 {
		t.Fatalf("hunks = %d, want 1", len(fd.Hunks))
	}
	if !hasLine(fd.Hunks[0].Lines, "del", "two") || !hasLine(fd.Hunks[0].Lines, "add", "TWO") {
		t.Errorf("hunk lines = %+v, want a del two and an add TWO", fd.Hunks[0].Lines)
	}
}

func TestDiffUntracked(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "unt.txt", "line1\nline2\n")

	r := NewRunner(allowPolicy(root))
	fd, err := r.Diff(context.Background(), repo, "unt.txt", ScopeUncommitted, "")
	if err != nil {
		t.Fatal(err)
	}
	if fd.Status != statusUntracked || fd.Binary {
		t.Errorf("status/binary = %q/%v, want U/false", fd.Status, fd.Binary)
	}
	if len(fd.Hunks) != 1 || len(fd.Hunks[0].Lines) != 2 {
		t.Fatalf("hunks = %+v, want one hunk of 2 lines", fd.Hunks)
	}
	if fd.Hunks[0].Lines[0].Kind != "add" || fd.Hunks[0].Lines[0].New != 1 {
		t.Errorf("first line = %+v, want add at new 1", fd.Hunks[0].Lines[0])
	}
}

func TestDiffBinaryUntracked(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeBytes(t, repo, "bin.dat", []byte{0, 1, 2, 3})

	r := NewRunner(allowPolicy(root))
	fd, err := r.Diff(context.Background(), repo, "bin.dat", ScopeUncommitted, "")
	if err != nil {
		t.Fatal(err)
	}
	if fd.Status != statusUntracked || !fd.Binary {
		t.Errorf("status/binary = %q/%v, want U/true", fd.Status, fd.Binary)
	}
	if len(fd.Hunks) != 0 {
		t.Errorf("hunks = %+v, want none", fd.Hunks)
	}
	if fd.Hunks == nil {
		t.Errorf("hunks is nil, want []")
	}
}

func TestDiffDeleted(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "f.txt", "gone\n")
	runGit(t, repo, "add", "f.txt")
	runGit(t, repo, "commit", "-qm", "add")
	if err := removeFile(repo, "f.txt"); err != nil {
		t.Fatal(err)
	}

	r := NewRunner(allowPolicy(root))
	fd, err := r.Diff(context.Background(), repo, "f.txt", ScopeUncommitted, "")
	if err != nil {
		t.Fatal(err)
	}
	if fd.Status != statusDeleted {
		t.Errorf("status = %q, want D", fd.Status)
	}
	if !hasLine(fd.Hunks[0].Lines, "del", "gone") {
		t.Errorf("hunk lines = %+v, want a del gone", fd.Hunks[0].Lines)
	}
}

func TestDiffLastCommit(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "f.txt", "one\n")
	runGit(t, repo, "add", "f.txt")
	runGit(t, repo, "commit", "-qm", "c2")
	writeFile(t, repo, "f.txt", "two\n")
	runGit(t, repo, "add", "f.txt")
	runGit(t, repo, "commit", "-qm", "c3")

	r := NewRunner(allowPolicy(root))
	fd, err := r.Diff(context.Background(), repo, "f.txt", ScopeLastCommit, "")
	if err != nil {
		t.Fatal(err)
	}
	if fd.Status != statusModified {
		t.Errorf("status = %q, want M", fd.Status)
	}
	if !hasLine(fd.Hunks[0].Lines, "add", "two") {
		t.Errorf("hunk lines = %+v, want an add two", fd.Hunks[0].Lines)
	}
}

func TestDiffBranch(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	runGit(t, repo, "branch", "development")
	runGit(t, repo, "checkout", "-q", "-b", "feature")
	writeFile(t, repo, "f.txt", "one\n")
	runGit(t, repo, "add", "f.txt")
	runGit(t, repo, "commit", "-qm", "c2")
	writeFile(t, repo, "f.txt", "two\n") // uncommitted change

	r := NewRunner(allowPolicy(root))
	fd, err := r.Diff(context.Background(), repo, "f.txt", ScopeBranch, "development")
	if err != nil {
		t.Fatal(err)
	}
	if len(fd.Hunks) == 0 {
		t.Fatalf("hunks is empty, want the working tree content")
	}
	if !hasLine(fd.Hunks[0].Lines, "add", "two") {
		t.Errorf("hunk lines = %+v, want the uncommitted content two", fd.Hunks[0].Lines)
	}
}

func TestDiffRejectsEscape(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	r := NewRunner(allowPolicy(root))

	for _, p := range []string{"", "../x", "/etc/passwd", "a/../../b"} {
		if _, err := r.Diff(context.Background(), repo, p, ScopeUncommitted, ""); err == nil {
			t.Errorf("Diff(%q) = nil error, want error", p)
		}
	}
}

func TestDiffInvalidScope(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "f.txt", "one\n")

	r := NewRunner(allowPolicy(root))
	if _, err := r.Diff(context.Background(), repo, "f.txt", Scope("head"), ""); err == nil {
		t.Fatal("Diff(unknown scope) = nil error, want error")
	}
}

// hasLine reports whether a hunk holds a line of the given kind and text.
func hasLine(lines []Line, kind, text string) bool {
	for _, l := range lines {
		if l.Kind == kind && l.Text == text {
			return true
		}
	}
	return false
}
