package scriptrt

import (
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
	"github.com/dop251/goja"
)

// installHTML defines the html(str) global (API-SP-009): it parses the string
// once with goquery and returns a selection. It fetches nothing.
func installHTML(vm *goja.Runtime) {
	_ = vm.Set("html", func(call goja.FunctionCall) goja.Value {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(call.Argument(0).String()))
		if err != nil {
			panic(vm.NewTypeError("html: %s", err.Error()))
		}
		return vm.ToValue(wrapSelection(vm, doc.Selection))
	})
}

// wrapSelection builds a JS-facing selection around a goquery selection.
func wrapSelection(vm *goja.Runtime, sel *goquery.Selection) *htmlSelection {
	return &htmlSelection{vm: vm, sel: sel, Length: sel.Length()}
}

// htmlSelection is the jQuery-like object html() returns. Its methods and the
// tagged length field are exposed lower-cased by the runtime's field name
// mapper. A bad CSS selector in find() throws a JS TypeError.
type htmlSelection struct {
	vm     *goja.Runtime
	sel    *goquery.Selection
	Length int `json:"length"`
}

// wrap builds a child selection for one goquery selection.
func (h *htmlSelection) wrap(sel *goquery.Selection) *htmlSelection {
	return &htmlSelection{vm: h.vm, sel: sel, Length: sel.Length()}
}

// Find returns the descendants matching a CSS selector.
func (h *htmlSelection) Find(css string) *htmlSelection {
	if _, err := cascadia.Compile(css); err != nil {
		panic(h.vm.NewTypeError("invalid selector: %s", css))
	}
	return h.wrap(h.sel.Find(css))
}

// Text returns the trimmed text of the selection.
func (h *htmlSelection) Text() string {
	return strings.TrimSpace(h.sel.Text())
}

// Attr returns an attribute value, or null when the attribute is absent.
func (h *htmlSelection) Attr(name string) any {
	if v, ok := h.sel.Attr(name); ok {
		return v
	}
	return nil
}

// Html returns the inner HTML of the first matched node.
func (h *htmlSelection) Html() string {
	s, err := h.sel.Html()
	if err != nil {
		return ""
	}
	return s
}

// First returns a selection holding the first matched node.
func (h *htmlSelection) First() *htmlSelection {
	return h.wrap(h.sel.First())
}

// Eq returns a selection holding the i-th matched node.
func (h *htmlSelection) Eq(i int) *htmlSelection {
	return h.wrap(h.sel.Eq(i))
}

// Map calls fn(selection, index) for every matched node and returns the results
// as a JS array.
func (h *htmlSelection) Map(call goja.FunctionCall) goja.Value {
	fn, ok := goja.AssertFunction(call.Argument(0))
	if !ok {
		panic(h.vm.NewTypeError("map: argument is not a function"))
	}
	out := make([]goja.Value, 0, h.sel.Length())
	h.sel.Each(func(i int, node *goquery.Selection) {
		res, err := fn(goja.Undefined(), h.vm.ToValue(h.wrap(node)), h.vm.ToValue(i))
		if err != nil {
			panic(err)
		}
		out = append(out, res)
	})
	return h.vm.ToValue(out)
}

// ToArray returns the matched nodes as a JS array of selections.
func (h *htmlSelection) ToArray() goja.Value {
	out := make([]*htmlSelection, 0, h.sel.Length())
	h.sel.Each(func(_ int, node *goquery.Selection) {
		out = append(out, h.wrap(node))
	})
	return h.vm.ToValue(out)
}
