package lana

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestKnowledgeSaveText checks the text→item conversion: markdown ext, the first
// line as title, the explicit title winning, and the 80-rune cap
// (REQ-KNU-012).
func TestKnowledgeSaveText(t *testing.T) {
	item, err := knowledgeSaveIngest(context.Background(), KnowledgeSave{Text: "First line\nsecond line"})
	if err != nil {
		t.Fatal(err)
	}
	if item.Ext != "md" || string(item.Data) != "First line\nsecond line" || item.Title != "First line" {
		t.Fatalf("item = %+v", item)
	}

	long := strings.Repeat("x", 100)
	item, err = knowledgeSaveIngest(context.Background(), KnowledgeSave{Text: long})
	if err != nil {
		t.Fatal(err)
	}
	if got := len([]rune(item.Title)); got != knowledgeIngestTitleMax {
		t.Fatalf("title runes = %d, want %d", got, knowledgeIngestTitleMax)
	}

	item, err = knowledgeSaveIngest(context.Background(), KnowledgeSave{Title: "Given", Text: "body"})
	if err != nil {
		t.Fatal(err)
	}
	if item.Title != "Given" {
		t.Fatalf("title = %q, want Given", item.Title)
	}
}

// TestKnowledgeSaveURL checks the URL path: the fetched page is stored as
// markdown with a source_url frontmatter block and its title (REQ-KNU-012).
func TestKnowledgeSaveURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><head><title>Example page</title></head><body><p>Hello</p></body></html>"))
	}))
	defer srv.Close()

	item, err := knowledgeSaveIngest(context.Background(), KnowledgeSave{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if item.Ext != "md" || item.SourceURL != srv.URL {
		t.Fatalf("item = %+v", item)
	}
	if item.Title != "Example page" {
		t.Fatalf("title = %q, want Example page", item.Title)
	}
	if !strings.HasPrefix(string(item.Data), "---\nsource_url: ") || !strings.Contains(string(item.Data), "\n---\n\n") {
		t.Fatalf("data has no source_url frontmatter: %q", item.Data)
	}
	if !strings.Contains(string(item.Data), "Hello") {
		t.Fatalf("data = %q", item.Data)
	}
}

// TestKnowledgeSaveDocument checks a document passthrough: the given extension,
// title, and bytes are kept (REQ-KNU-012).
func TestKnowledgeSaveDocument(t *testing.T) {
	item, err := knowledgeSaveIngest(context.Background(), KnowledgeSave{Title: "report", Ext: ".pdf", Data: []byte("%PDF")})
	if err != nil {
		t.Fatal(err)
	}
	if item.Ext != ".pdf" || item.Title != "report" || string(item.Data) != "%PDF" {
		t.Fatalf("item = %+v", item)
	}
}
