package web

import (
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// gzipWriterPool recycles gzip writers between requests. BestSpeed favors
// latency over ratio, which suits the chat-sized JSON payloads the middleware
// compresses.
var gzipWriterPool = sync.Pool{
	New: func() any {
		w, err := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		if err != nil {
			w = gzip.NewWriter(io.Discard)
		}
		return w
	},
}

// compressibleContentTypes are the response content types the middleware
// compresses, without their parameters. text/event-stream is deliberately
// absent: a stream must reach the client uncompressed and unbuffered.
var compressibleContentTypes = map[string]bool{
	"application/json":          true,
	"text/html":                 true,
	"text/css":                  true,
	"text/javascript":           true,
	"application/javascript":    true,
	"image/svg+xml":             true,
	"text/plain":                true,
	"application/manifest+json": true,
}

// compress gzips a response when the client allows it and the response carries
// a compressible body: a 200 status and a compressible content type, without a
// prior Content-Encoding or a Content-Range. HEAD requests and protocol
// upgrades (WebSocket) pass through untouched so the handler keeps the raw
// ResponseWriter and its Hijack. The decision is made when the handler writes
// its header, so a stream or an error answer is never buffered.
func (s *Server) compress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead || isUpgrade(r) || !acceptsGzip(r.Header.Get("Accept-Encoding")) {
			next.ServeHTTP(w, r)
			return
		}
		cw := &compressWriter{ResponseWriter: w}
		defer cw.close()
		next.ServeHTTP(cw, r)
	})
}

// compressWriter wraps a ResponseWriter and swaps in a gzip writer once the
// response is known to be compressible. It forwards Flush and exposes Unwrap so
// http.ResponseController reaches the underlying writer.
type compressWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	gz          *gzip.Writer
}

// WriteHeader records the status and, for a compressible response, installs the
// gzip writer and the Content-Encoding header before writing the header
// through to the underlying writer. A second call is ignored, matching the
// stdlib behavior.
func (w *compressWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	if compressibleResponse(w.status, w.Header()) {
		header := w.Header()
		header.Set("Content-Encoding", "gzip")
		header.Del("Content-Length")
		header.Add("Vary", "Accept-Encoding")
		gz := gzipWriterPool.Get().(*gzip.Writer)
		gz.Reset(w.ResponseWriter)
		w.gz = gz
	}
	w.ResponseWriter.WriteHeader(status)
}

// Write sends the body through the gzip writer once compression is on, and to
// the underlying writer otherwise. The first call implies a 200 header.
func (w *compressWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.gz != nil {
		return w.gz.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

// Flush flushes the gzip writer, then the underlying writer when it supports
// flushing, so a streaming handler still reaches the client promptly.
func (w *compressWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the wrapped writer so http.ResponseController can reach its
// methods.
func (w *compressWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// close flushes the gzip trailer and returns the writer to the pool. It is a
// no-op when the response was never compressed.
func (w *compressWriter) close() {
	if w.gz == nil {
		return
	}
	_ = w.gz.Close()
	gzipWriterPool.Put(w.gz)
	w.gz = nil
}

// compressibleResponse reports whether a response with the given status and
// headers is a candidate for gzip.
func compressibleResponse(status int, header http.Header) bool {
	if status != http.StatusOK {
		return false
	}
	if header.Get("Content-Encoding") != "" || header.Get("Content-Range") != "" {
		return false
	}
	return compressibleContentType(header.Get("Content-Type"))
}

// compressibleContentType reports whether a Content-Type value, parameters such
// as charset included, is in the compressible set.
func compressibleContentType(value string) bool {
	if i := strings.IndexByte(value, ';'); i >= 0 {
		value = value[:i]
	}
	return compressibleContentTypes[strings.ToLower(strings.TrimSpace(value))]
}

// isUpgrade reports whether r asks for a protocol upgrade. Such a request
// bypasses the wrapper so the handler can hijack the connection, as
// coder/websocket does.
func isUpgrade(r *http.Request) bool {
	if r.Header.Get("Upgrade") != "" {
		return true
	}
	for _, token := range strings.Split(r.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
			return true
		}
	}
	return false
}

// acceptsGzip reports whether an Accept-Encoding value allows a gzip response.
// It parses the header as a list of codings with optional q weights; a gzip or
// "*" coding with q=0 is a rejection (RFC 9110 section 12.5.3).
func acceptsGzip(header string) bool {
	wildcardQ := -1.0
	for _, coding := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(coding, ";")
		name = strings.TrimSpace(name)
		switch {
		case strings.EqualFold(name, "gzip"):
			// An explicit gzip coding wins over a wildcard.
			return parseQuality(params) > 0
		case name == "*" && wildcardQ < 0:
			wildcardQ = parseQuality(params)
		}
	}
	return wildcardQ > 0
}

// parseQuality returns the q weight of an Accept-Encoding coding's parameters,
// defaulting to 1 when none is given or it does not parse.
func parseQuality(params string) float64 {
	for _, param := range strings.Split(params, ";") {
		key, value, ok := strings.Cut(param, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "q") {
			continue
		}
		q, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return 1
		}
		return q
	}
	return 1
}
