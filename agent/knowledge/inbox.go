package knowledge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"golang.org/x/net/html"

	"github.com/ridhomujizat/lana-harness/core"
)

// Inbox bounds (REQ-KNU-010, API-KNU-005).
const (
	inboxMaxBytes = 30 << 20
	inboxFreshAge = 10 * time.Second
)

// inboxExts are the inbox file types the owner can drop or upload
// (REQ-KNU-010, API-KNU-005). .html is converted to markdown before ingest.
var inboxExts = map[string]bool{".md": true, ".txt": true, ".pdf": true, ".html": true}

// scanInbox ingests the regular files directly in inbox/, removes the ones that
// became a source, lists the unsupported ones, and skips files changed in the
// last 10 seconds (REQ-KNU-010, design §6).
func (s *Service) scanInbox(ctx context.Context) error {
	vault, _, err := s.open()
	if err != nil {
		return err
	}
	dir := filepath.Join(vault.Root(), "inbox")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}

	agentName, _ := knowledgeAgent(s.Config)
	now := s.now()
	var (
		unsupported []string
		errs        []error
	)
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		name := entry.Name()
		info, err := entry.Info()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if !inboxExts[ext] {
			unsupported = append(unsupported, "inbox/"+name)
			continue
		}
		abs := filepath.Join(dir, name)
		if !s.takeFreshInbox(abs) && now.Sub(info.ModTime()) < inboxFreshAge {
			continue // REQ-KNU-010: a file changed in the last 10 s waits
		}
		if agentName == "" {
			// REQ-KNU-030: without a knowledge agent the file stays put; the
			// reason is reported through Status.
			continue
		}
		if err := s.ingestInboxFile(ctx, abs, name, ext, agentName); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	s.setInboxUnsupported(unsupported)

	s.upkeep.mu.Lock()
	s.upkeep.lastScan = now
	s.upkeep.scanRequested = false
	s.upkeep.mu.Unlock()
	return errors.Join(errs...)
}

// ingestInboxFile reads one supported inbox file and ingests it as a source; an
// HTML file is converted to markdown first (REQ-KNU-010, design §6).
func (s *Service) ingestInboxFile(ctx context.Context, abs, name, ext, agentName string) error {
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	title := strings.TrimSuffix(name, filepath.Ext(name))
	storeExt := strings.TrimPrefix(ext, ".")
	if ext == ".html" {
		markdown, err := htmlToMarkdown(data)
		if err != nil {
			return err
		}
		data = []byte(markdown)
		storeExt = "md"
	}
	_, err = s.Ingest(ctx, core.KnowledgeIngest{Title: title, Ext: storeExt, Data: data, AgentName: agentName})
	return err
}

// SaveInbox validates and writes one uploaded file under inbox/, then requests
// a scan with the 10 s age rule waived for it (API-KNU-005, REQ-KNU-011,
// design §6).
func (s *Service) SaveInbox(ctx context.Context, name string, data []byte) (string, error) {
	clean := filepath.Base(strings.TrimSpace(name))
	if clean == "" || clean == "." || clean == string(filepath.Separator) {
		return "", errors.New("file name is required")
	}
	ext := strings.ToLower(filepath.Ext(clean))
	if !inboxExts[ext] {
		return "", fmt.Errorf("unsupported file type %s", ext)
	}
	if len(data) > inboxMaxBytes {
		return "", fmt.Errorf("file is larger than %d bytes", inboxMaxBytes)
	}

	vault, _, err := s.open()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(vault.Root(), "inbox")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	abs, err := freeInboxName(dir, clean)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, data, 0o600); err != nil {
		return "", err
	}
	s.markFreshInbox(abs)
	s.ScanInboxNow()
	return "inbox/" + filepath.Base(abs), nil
}

// freeInboxName picks inbox/<name>, adding " (2)", " (3)", … on a name clash
// (API-KNU-005).
func freeInboxName(dir, name string) (string, error) {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		candidate := base
		if i > 1 {
			candidate = fmt.Sprintf("%s (%d)", base, i)
		}
		abs := filepath.Join(dir, candidate+ext)
		_, err := os.Stat(abs)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return abs, nil
		case err != nil:
			return "", err
		}
	}
}

// takeFreshInbox reports whether abs was written by SaveInbox and clears the
// flag, so the scan skips the 10 s age rule once for it (design §6).
func (s *Service) takeFreshInbox(abs string) bool {
	s.upkeep.mu.Lock()
	defer s.upkeep.mu.Unlock()
	if s.upkeep.justSaved[abs] {
		delete(s.upkeep.justSaved, abs)
		return true
	}
	return false
}

// markFreshInbox records that SaveInbox wrote abs.
func (s *Service) markFreshInbox(abs string) {
	s.upkeep.mu.Lock()
	if s.upkeep.justSaved == nil {
		s.upkeep.justSaved = map[string]bool{}
	}
	s.upkeep.justSaved[abs] = true
	s.upkeep.mu.Unlock()
}

// htmlToMarkdown converts an HTML document to markdown, dropping script, style,
// and noscript elements first (the same conversion spec 30 uses, design §6).
func htmlToMarkdown(data []byte) (string, error) {
	cleaned, err := stripHTMLDocument(data)
	if err != nil {
		return "", err
	}
	return htmltomarkdown.ConvertString(cleaned)
}

// stripHTMLDocument removes script, style, and noscript elements and renders
// the remaining document as HTML text.
func stripHTMLDocument(body []byte) (string, error) {
	node, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "noscript") {
			if n.Parent != nil {
				n.Parent.RemoveChild(n)
			}
			return
		}
		for child := n.FirstChild; child != nil; {
			next := child.NextSibling
			walk(child)
			child = next
		}
	}
	walk(node)
	var buf bytes.Buffer
	if err := html.Render(&buf, node); err != nil {
		return "", err
	}
	return buf.String(), nil
}
