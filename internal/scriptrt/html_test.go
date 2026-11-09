package scriptrt

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// reposHTML is the fixture for the html() tests: a list of links.
const reposHTML = `<ul class="repos">
  <li class="repo"><a href="/r/alpha">Alpha</a></li>
  <li class="repo"><a href="/r/beta">Beta</a></li>
  <li class="repo"><a href="/r/gamma">Gamma</a></li>
</ul>`

// TestHTMLFind exercises find, text, attr, map, eq, toArray and length.
func TestHTMLFind(t *testing.T) {
	p := mustCompile(t, `function parse(input, ctx){
		var doc = html(input.html);
		var items = doc.find("li");
		return {
			count: items.length,
			texts: items.map(function(el, i){ return el.text(); }).join(","),
			firstHref: items.first().find("a").attr("href"),
			secondText: items.eq(1).text(),
			missingAttr: items.first().attr("nope") === null,
			arrayLen: items.toArray().length,
			innerHtml: items.first().html()
		};
	}`)
	got := runObject(t, p, map[string]any{"html": reposHTML}, Options{})
	checks := []struct {
		key  string
		want any
	}{
		{"count", float64(3)},
		{"texts", "Alpha,Beta,Gamma"},
		{"firstHref", "/r/alpha"},
		{"secondText", "Beta"},
		{"missingAttr", true},
		{"arrayLen", float64(3)},
		{"innerHtml", `<a href="/r/alpha">Alpha</a>`},
	}
	for _, c := range checks {
		if got[c.key] != c.want {
			t.Fatalf("%s = %#v, want %#v", c.key, got[c.key], c.want)
		}
	}
}

// TestHTMLInvalidSelector checks a bad CSS selector throws a JS TypeError that a
// script can catch.
func TestHTMLInvalidSelector(t *testing.T) {
	p := mustCompile(t, `function parse(input, ctx){
		var doc = html("<p>x</p>");
		try { doc.find(">>>"); return {err: "none"}; }
		catch (e) { return {err: e.name}; }
	}`)
	got := runObject(t, p, nil, Options{})
	if got["err"] != "TypeError" {
		t.Fatalf("err = %#v, want TypeError", got["err"])
	}
}

// TestHTMLInvalidSelectorUncaught checks an uncaught bad selector surfaces as a
// *ScriptError.
func TestHTMLInvalidSelectorUncaught(t *testing.T) {
	p := mustCompile(t, `function parse(input, ctx){ return {n: html("<p>x</p>").find(">>>").length}; }`)
	_, err := Run(context.Background(), p, nil, nil, Options{})
	var se *ScriptError
	if !errors.As(err, &se) {
		t.Fatalf("Run err = %v, want *ScriptError", err)
	}
	if !strings.Contains(se.Message, "TypeError") {
		t.Fatalf("ScriptError.Message = %q, want a TypeError", se.Message)
	}
}
