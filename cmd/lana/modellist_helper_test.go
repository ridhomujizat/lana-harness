package main

import (
	"net/http"
	"strings"
)

// skipModelList answers the provider model-list GET the router sends to warm
// its model facts (REQ-MDL-010) with 404, so fake LLM servers in these tests
// only see chat requests. It reports whether the request was handled.
func skipModelList(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models") {
		http.Error(w, "no model list", http.StatusNotFound)
		return true
	}
	return false
}
