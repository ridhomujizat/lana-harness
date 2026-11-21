package gitinfo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTreeOrderingAndGitHidden(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "A.txt", "a\n")
	writeFile(t, repo, "b.txt", "b\n")
	writeFile(t, repo, "src/main.go", "package main\n")
	writeFile(t, repo, "Zeta/notes.md", "hello\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-qm", "tree")

	r := NewRunner(allowPolicy(root))
	list, err := r.Tree(context.Background(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if list.Dir != "" || list.Truncated {
		t.Errorf("dir/truncated = %q/%v, want \"\"/false", list.Dir, list.Truncated)
	}
	got := make([]string, len(list.Entries))
	for i, e := range list.Entries {
		got[i] = e.Kind + ":" + e.Name
	}
	want := []string{"dir:src", "dir:Zeta", "file:A.txt", "file:b.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("entries = %v, want %v", got, want)
	}
	for _, e := range list.Entries {
		if e.Name == ".git" {
			t.Errorf(".git is listed in the tree")
		}
		if e.Ignored {
			t.Errorf("%s is marked ignored, want false", e.Name)
		}
		if e.Kind == treeFileKind && e.Size == 0 {
			t.Errorf("%s size = 0, want the file size", e.Name)
		}
	}
}

func TestTreeHidesGitInFolder(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "sub/keep.txt", "x\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-qm", "sub")
	if err := os.MkdirAll(filepath.Join(repo, "sub", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := NewRunner(allowPolicy(root))
	list, err := r.Tree(context.Background(), repo, "sub")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range list.Entries {
		if e.Name == ".git" {
			t.Errorf(".git is listed in a folder")
		}
	}
	if len(list.Entries) != 1 || list.Entries[0].Name != "keep.txt" {
		t.Errorf("entries = %+v, want only keep.txt", list.Entries)
	}
}

func TestTreeIgnored(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, ".gitignore", "build/\n*.log\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-qm", "ignore")
	writeFile(t, repo, "build/out.txt", "out\n")
	writeFile(t, repo, "debug.log", "log\n")
	writeFile(t, repo, "keep.txt", "keep\n")

	r := NewRunner(allowPolicy(root))
	list, err := r.Tree(context.Background(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]TreeEntry{}
	for _, e := range list.Entries {
		byName[e.Name] = e
	}
	if e := byName["build"]; e.Kind != treeDirKind || !e.Ignored {
		t.Errorf("build = %+v, want a dir marked ignored", e)
	}
	if e := byName["debug.log"]; !e.Ignored {
		t.Errorf("debug.log = %+v, want ignored", e)
	}
	if e := byName["keep.txt"]; e.Ignored {
		t.Errorf("keep.txt = %+v, want not ignored", e)
	}
}

func TestTreeChangedFolder(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "src/deep/f.txt", "one\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-qm", "deep")
	writeFile(t, repo, "src/deep/f.txt", "two\n") // modified deep inside

	r := NewRunner(allowPolicy(root))
	rootList, err := r.Tree(context.Background(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if e := entryNamed(rootList, "src"); !e.Changed {
		t.Errorf("src changed = false, want true")
	}
	midList, err := r.Tree(context.Background(), repo, "src")
	if err != nil {
		t.Fatal(err)
	}
	if e := entryNamed(midList, "deep"); !e.Changed {
		t.Errorf("src/deep changed = false, want true")
	}
	deepList, err := r.Tree(context.Background(), repo, "src/deep")
	if err != nil {
		t.Fatal(err)
	}
	if e := entryNamed(deepList, "f.txt"); e.Status != statusModified {
		t.Errorf("src/deep/f.txt status = %q, want M", e.Status)
	}
}

func TestTreeSymlinkNotFollowed(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "realdir/inner.txt", "x\n")
	if err := os.Symlink("realdir", filepath.Join(repo, "linkdir")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	r := NewRunner(allowPolicy(root))
	list, err := r.Tree(context.Background(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if e := entryNamed(list, "linkdir"); e.Kind != treeFileKind {
		t.Errorf("linkdir kind = %q, want file", e.Kind)
	}
	if e := entryNamed(list, "realdir"); e.Kind != treeDirKind {
		t.Errorf("realdir kind = %q, want dir", e.Kind)
	}
}

func TestTreeTruncation(t *testing.T) {
	old := treeMaxEntries
	treeMaxEntries = 3
	t.Cleanup(func() { treeMaxEntries = old })

	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	for _, name := range []string{"f1.txt", "f2.txt", "f3.txt", "f4.txt", "f5.txt"} {
		writeFile(t, repo, name, name+"\n")
	}
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-qm", "many")

	r := NewRunner(allowPolicy(root))
	list, err := r.Tree(context.Background(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if !list.Truncated {
		t.Errorf("truncated = false, want true")
	}
	if len(list.Entries) != treeMaxEntries {
		t.Errorf("entries = %d, want %d", len(list.Entries), treeMaxEntries)
	}
	if list.Entries[0].Name != "f1.txt" {
		t.Errorf("first entry = %q, want f1.txt", list.Entries[0].Name)
	}
}

func TestTreeEmptyFolder(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	if err := os.MkdirAll(filepath.Join(repo, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := NewRunner(allowPolicy(root))
	list, err := r.Tree(context.Background(), repo, "empty")
	if err != nil {
		t.Fatal(err)
	}
	if list.Entries == nil {
		t.Fatal("entries is nil, want []")
	}
	if len(list.Entries) != 0 {
		t.Errorf("entries = %+v, want none", list.Entries)
	}
}

func TestTreeMissingFolder(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	r := NewRunner(allowPolicy(root))

	if _, err := r.Tree(context.Background(), repo, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Tree(missing) = %v, want ErrNotFound", err)
	}
}

func TestTreeEscapeRejected(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	r := NewRunner(allowPolicy(root))

	for _, dir := range []string{"../x", "a/../../b", "/etc"} {
		if _, err := r.Tree(context.Background(), repo, dir); !errors.Is(err, ErrForbiddenPath) {
			t.Errorf("Tree(%q) = %v, want ErrForbiddenPath", dir, err)
		}
	}
}

func TestSearchRanking(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "zab.txt", "1\n")
	writeFile(t, repo, "ab/x.txt", "2\n")
	writeFile(t, repo, "axb.txt", "3\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-qm", "search")

	r := NewRunner(allowPolicy(root))
	got, err := r.Search(context.Background(), repo, "ab")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"zab.txt", "ab/x.txt", "axb.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("search = %v, want %v", got, want)
	}
}

func TestSearchEmptyAndNoMatch(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "f.txt", "x\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-qm", "f")

	r := NewRunner(allowPolicy(root))
	for _, q := range []string{"", "   ", "zzzz"} {
		got, err := r.Search(context.Background(), repo, q)
		if err != nil {
			t.Fatalf("Search(%q) error = %v", q, err)
		}
		if got == nil {
			t.Errorf("Search(%q) = nil, want []", q)
		}
		if len(got) != 0 {
			t.Errorf("Search(%q) = %v, want empty", q, got)
		}
	}
}

func TestSearchCap(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	for i := 0; i < maxSearchResults+10; i++ {
		writeFile(t, repo, filepath.Join("m", "m"+twoDigits(i)+".txt"), "x\n")
	}
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-qm", "many")

	r := NewRunner(allowPolicy(root))
	got, err := r.Search(context.Background(), repo, "m")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != maxSearchResults {
		t.Errorf("search = %d results, want %d", len(got), maxSearchResults)
	}
}

func TestSearchExcludesIgnored(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, ".gitignore", "*.log\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-qm", "ignore")
	writeFile(t, repo, "secret.log", "s\n")

	r := NewRunner(allowPolicy(root))
	got, err := r.Search(context.Background(), repo, "secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range got {
		if strings.Contains(path, "secret") {
			t.Errorf("ignored file returned: %v", got)
		}
	}
	if len(got) != 0 {
		t.Errorf("search = %v, want empty", got)
	}
}

func TestFileGoText(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	content := "package main\n\nfunc main() {}\n"
	writeFile(t, repo, "main.go", content)

	r := NewRunner(allowPolicy(root))
	fc, err := r.File(context.Background(), repo, "main.go")
	if err != nil {
		t.Fatal(err)
	}
	if fc.Kind != "text" || fc.Path != "main.go" {
		t.Errorf("kind/path = %q/%q, want text/main.go", fc.Kind, fc.Path)
	}
	if fc.Text != content {
		t.Errorf("text = %q, want %q", fc.Text, content)
	}
	if fc.Lines != 3 {
		t.Errorf("lines = %d, want 3", fc.Lines)
	}
	if fc.Language != "go" {
		t.Errorf("language = %q, want go", fc.Language)
	}
	if fc.Size != int64(len(content)) {
		t.Errorf("size = %d, want %d", fc.Size, len(content))
	}
}

func TestFileNoTrailingNewline(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "x.go", "a\nb")

	r := NewRunner(allowPolicy(root))
	fc, err := r.File(context.Background(), repo, "x.go")
	if err != nil {
		t.Fatal(err)
	}
	if fc.Lines != 2 {
		t.Errorf("lines = %d, want 2", fc.Lines)
	}
}

func TestFileEmpty(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "empty.txt", "")

	r := NewRunner(allowPolicy(root))
	fc, err := r.File(context.Background(), repo, "empty.txt")
	if err != nil {
		t.Fatal(err)
	}
	if fc.Kind != "text" || fc.Lines != 0 || fc.Text != "" {
		t.Errorf("file = %+v, want empty text with 0 lines", fc)
	}
}

func TestFileTypeScript(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "a.ts", "export const x = 1\n")

	r := NewRunner(allowPolicy(root))
	fc, err := r.File(context.Background(), repo, "a.ts")
	if err != nil {
		t.Fatal(err)
	}
	if fc.Language != "typescript" || fc.Kind != "text" {
		t.Errorf("language/kind = %q/%q, want typescript/text", fc.Language, fc.Kind)
	}
}

func TestFileBinary(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeBytes(t, repo, "bin.dat", []byte{0, 1, 2, 3})

	r := NewRunner(allowPolicy(root))
	fc, err := r.File(context.Background(), repo, "bin.dat")
	if err != nil {
		t.Fatal(err)
	}
	if fc.Kind == "text" {
		t.Errorf("kind = %q, want a non-text kind", fc.Kind)
	}
	if fc.Text != "" || fc.Lines != 0 {
		t.Errorf("file = %+v, want no text", fc)
	}
	if fc.Size != 4 {
		t.Errorf("size = %d, want 4", fc.Size)
	}
}

func TestFileLargeTextHasNoBody(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	data := strings.Repeat("a\n", (maxFileTextSize/2)+10)
	writeFile(t, repo, "big.go", data)

	r := NewRunner(allowPolicy(root))
	fc, err := r.File(context.Background(), repo, "big.go")
	if err != nil {
		t.Fatal(err)
	}
	if fc.Kind != "text" {
		t.Errorf("kind = %q, want text", fc.Kind)
	}
	if fc.Text != "" || fc.Lines != 0 {
		t.Errorf("file = %+v, want size only", fc)
	}
}

func TestFileMissingAndDir(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "dir/keep.txt", "x\n")

	r := NewRunner(allowPolicy(root))
	if _, err := r.File(context.Background(), repo, "nope.txt"); !errors.Is(err, ErrNotFound) {
		t.Errorf("File(missing) = %v, want ErrNotFound", err)
	}
	if _, err := r.File(context.Background(), repo, "dir"); !errors.Is(err, ErrNotFound) {
		t.Errorf("File(dir) = %v, want ErrNotFound", err)
	}
}

func TestFileEscapeRejected(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	r := NewRunner(allowPolicy(root))

	for _, path := range []string{"../x", "/etc/passwd", "a/../../b"} {
		if _, err := r.File(context.Background(), repo, path); !errors.Is(err, ErrForbiddenPath) {
			t.Errorf("File(%q) = %v, want ErrForbiddenPath", path, err)
		}
	}
}

// entryNamed returns the entry of a list with the given name.
func entryNamed(list TreeList, name string) TreeEntry {
	for _, e := range list.Entries {
		if e.Name == name {
			return e
		}
	}
	return TreeEntry{}
}

// twoDigits pads a number below 100 to two digits, for stable file names.
func twoDigits(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}
