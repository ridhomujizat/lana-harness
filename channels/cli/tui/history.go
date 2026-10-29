package tui

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// History menyimpan riwayat input di berkas JSON Lines.
type History struct {
	mu      sync.Mutex
	path    string
	max     int
	entries []string
}

type historyEntry struct {
	Text string `json:"text"`
}

// LoadHistory membaca riwayat dari path. Path kosong berarti riwayat mati.
func LoadHistory(path string, max int) *History {
	h := &History{path: path, max: max}
	if path == "" || max <= 0 {
		return h
	}
	file, err := os.Open(path)
	if err != nil {
		return h
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var entry historyEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil || entry.Text == "" {
			continue
		}
		h.entries = append(h.entries, entry.Text)
	}
	if overflow := len(h.entries) - max; overflow > 0 {
		h.entries = h.entries[overflow:]
	}
	return h
}

// Add menambahkan entri dan menyimpannya.
func (h *History) Add(text string) {
	text = strings.TrimSpace(text)
	if text == "" || h.path == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if n := len(h.entries); n > 0 && h.entries[n-1] == text {
		return
	}
	h.entries = append(h.entries, text)
	overflow := len(h.entries) - h.max
	if overflow > 0 {
		h.entries = append([]string(nil), h.entries[overflow:]...)
		h.rewrite()
		return
	}
	h.appendLine(text)
}

func (h *History) appendLine(text string) {
	data, err := json.Marshal(historyEntry{Text: text})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(h.path), 0o700); err != nil {
		return
	}
	file, err := os.OpenFile(h.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	_, _ = file.Write(append(data, '\n'))
}

func (h *History) rewrite() {
	if err := os.MkdirAll(filepath.Dir(h.path), 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(h.path), "history-*")
	if err != nil {
		return
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	for _, entry := range h.entries {
		data, err := json.Marshal(historyEntry{Text: entry})
		if err != nil {
			continue
		}
		if _, err := tmp.Write(append(data, '\n')); err != nil {
			_ = tmp.Close()
			return
		}
	}
	if err := tmp.Close(); err != nil {
		return
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return
	}
	_ = os.Rename(name, h.path)
}

// Len adalah jumlah entri.
func (h *History) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.entries)
}

// At mengambil entri ke-i (0 paling lama).
func (h *History) At(i int) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if i < 0 || i >= len(h.entries) {
		return ""
	}
	return h.entries[i]
}

// Search mencari entri terakhir yang memuat query.
func (h *History) Search(query string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	query = strings.ToLower(strings.TrimSpace(query))
	for i := len(h.entries) - 1; i >= 0; i-- {
		if query == "" || strings.Contains(strings.ToLower(h.entries[i]), query) {
			return h.entries[i], true
		}
	}
	return "", false
}
