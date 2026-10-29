package config

import (
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// layerData is the parse result of a single layer.
type layerData struct {
	value     map[string]any
	positions map[string]pos
}

type pos struct {
	Line int
	Col  int
}

func parseTOML(data []byte, file string) (layerData, *Error) {
	var v map[string]any
	if err := toml.Unmarshal(data, &v); err != nil {
		line, col := 0, 0
		if de, ok := err.(*toml.DecodeError); ok {
			line, col = de.Position()
		}
		return layerData{value: map[string]any{}, positions: map[string]pos{}}, &Error{
			File: file,
			Line: line,
			Col:  col,
			Msg:  err.Error(),
		}
	}
	if v == nil {
		v = map[string]any{}
	}
	p := &unstable.Parser{}
	p.Reset(data)
	positions := map[string]pos{}
	var current []string
	arrayCount := map[string]int{}
	for p.NextExpression() {
		node := p.Expression()
		switch node.Kind {
		case unstable.Table:
			key, at := keyPath(p, node)
			current = key
			positions[strings.Join(key, ".")] = at
		case unstable.ArrayTable:
			key, at := keyPath(p, node)
			base := strings.Join(key, ".")
			idx := arrayCount[base]
			arrayCount[base] = idx + 1
			current = append(append([]string{}, key...), itoa(idx))
			positions[strings.Join(current, ".")] = at
			positions[base+"."+itoa(idx)] = at
		case unstable.KeyValue:
			key, at := keyPath(p, node)
			full := append(append([]string{}, current...), key...)
			positions[strings.Join(full, ".")] = at
			walkValue(p, node.Value(), full, positions)
		}
	}
	if err := p.Error(); err != nil {
		return layerData{value: v, positions: positions}, &Error{File: file, Line: 0, Msg: err.Error()}
	}
	return layerData{value: v, positions: positions}, nil
}

func keyPath(p *unstable.Parser, node *unstable.Node) ([]string, pos) {
	var parts []string
	first := pos{}
	it := node.Key()
	for it.Next() {
		key := it.Node()
		if len(parts) == 0 {
			shape := p.Shape(key.Raw)
			first = pos{Line: shape.Start.Line, Col: shape.Start.Column}
		}
		parts = append(parts, string(key.Data))
	}
	return parts, first
}

func walkValue(p *unstable.Parser, node *unstable.Node, path []string, out map[string]pos) {
	if node == nil {
		return
	}
	switch node.Kind {
	case unstable.InlineTable:
		out[strings.Join(path, ".")] = nodePos(p, node)
		it := node.Children()
		for it.Next() {
			child := it.Node()
			if child.Kind != unstable.KeyValue {
				continue
			}
			key, at := keyPath(p, child)
			full := append(append([]string{}, path...), key...)
			out[strings.Join(full, ".")] = at
			walkValue(p, child.Value(), full, out)
		}
	case unstable.Array:
		idx := 0
		it := node.Children()
		for it.Next() {
			child := it.Node()
			full := append(append([]string{}, path...), itoa(idx))
			out[strings.Join(full, ".")] = nodePos(p, child)
			walkValue(p, child, full, out)
			idx++
		}
	}
}

func nodePos(p *unstable.Parser, node *unstable.Node) pos {
	shape := p.Shape(node.Raw)
	return pos{Line: shape.Start.Line, Col: shape.Start.Column}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[n:])
}
