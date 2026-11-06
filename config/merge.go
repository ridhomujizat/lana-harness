package config

import (
	"strconv"
	"strings"
)

type loadedLayer struct {
	info LayerInfo
	data layerData
	rank int
}

type mergeState struct {
	value     map[string]any
	origins   Origins
	positions map[string]pos
}

func mergeLayers(layers []loadedLayer) mergeState {
	st := mergeState{
		value:     map[string]any{},
		origins:   Origins{},
		positions: map[string]pos{},
	}
	var newIDs []string
	newSet := map[string]bool{}
	for _, layer := range layers {
		name := layer.info.Name
		m := layer.data.value
		for _, key := range sortedKeys(m) {
			sv := m[key]
			switch key {
			case "modules":
				mergeModules(st.value, sv, layer.data.positions, name, &newIDs, newSet, &st)
				st.origins["modules"] = name
			case "routes":
				replaceRoutes(&st, sv, layer.data.positions, name)
			default:
				mergeTop(&st, key, sv, layer.data.positions, name)
			}
		}
	}
	if rows, ok := st.value["modules"].([]any); ok {
		st.value["modules"] = reorderModules(rows, newIDs)
	}
	return st
}

func mergeTop(st *mergeState, key string, sv any, srcPos map[string]pos, layer string) {
	if sm, ok := asMap(sv); ok {
		dm, _ := st.value[key].(map[string]any)
		if dm == nil {
			dm = map[string]any{}
			st.value[key] = dm
		}
		st.origins[key] = layer
		if p, ok := srcPos[key]; ok {
			st.positions[key] = p
		}
		mergeMap(dm, sm, key, srcPos, layer, st)
		return
	}
	st.value[key] = sv
	st.origins[key] = layer
	if p, ok := srcPos[key]; ok {
		st.positions[key] = p
	}
}

func mergeMap(dst, src map[string]any, prefix string, srcPos map[string]pos, layer string, st *mergeState) {
	for _, key := range sortedKeys(src) {
		path := prefix + "." + key
		sv := src[key]
		if sm, ok := asMap(sv); ok {
			dm, _ := dst[key].(map[string]any)
			if dm == nil {
				dm = map[string]any{}
				dst[key] = dm
			}
			st.origins[path] = layer
			if p, ok := srcPos[path]; ok {
				st.positions[path] = p
			}
			mergeMap(dm, sm, path, srcPos, layer, st)
			continue
		}
		dst[key] = sv
		st.origins[path] = layer
		if p, ok := srcPos[path]; ok {
			st.positions[path] = p
		}
	}
}

func mergeModules(dst map[string]any, raw any, srcPos map[string]pos, layer string, newIDs *[]string, newSet map[string]bool, st *mergeState) {
	list, _ := asSlice(raw)
	rows, ok := dst["modules"].([]any)
	if !ok {
		rows = []any{}
	}
	for i, item := range list {
		srcRow, ok := asMap(item)
		if !ok {
			continue
		}
		id, _ := srcRow["id"].(string)
		if id == "" {
			continue
		}
		srcPrefix := "modules." + strconv.Itoa(i)
		idx := -1
		for j, existing := range rows {
			row, _ := asMap(existing)
			if got, _ := row["id"].(string); got == id {
				idx = j
				break
			}
		}
		if idx < 0 {
			row := map[string]any{"id": id}
			for _, key := range sortedKeys(srcRow) {
				row[key] = srcRow[key]
				stCopyPos := srcPos[srcPrefix+"."+key]
				if stCopyPos != (pos{}) {
					st.positions["modules."+id+"."+key] = stCopyPos
				}
				st.origins["modules."+id+"."+key] = layer
			}
			st.origins["modules."+id+".id"] = layer
			if p, ok := srcPos[srcPrefix+".id"]; ok {
				st.positions["modules."+id+".id"] = p
			}
			if p, ok := srcPos[srcPrefix]; ok {
				st.positions["modules."+id] = p
			}
			rows = append(rows, row)
			st.origins["modules."+id] = layer
			if !newSet[id] {
				newSet[id] = true
				*newIDs = append(*newIDs, id)
			}
			continue
		}
		row, _ := asMap(rows[idx])
		for _, key := range sortedKeys(srcRow) {
			if key == "id" {
				continue
			}
			if key == "config" {
				deletePrefix(st.origins, "modules."+id+".config")
				deletePrefixPos(st.positions, "modules."+id+".config")
			}
			row[key] = srcRow[key]
			st.origins["modules."+id+"."+key] = layer
			if p, ok := srcPos[srcPrefix+"."+key]; ok {
				st.positions["modules."+id+"."+key] = p
			}
			if key == "config" {
				copyModuleChildPositions(st.positions, srcPos, srcPrefix+".config", "modules."+id+".config")
				copyModuleChildOrigins(st.origins, srcPrefix+".config", "modules."+id+".config", layer, srcPos)
			}
		}
		st.origins["modules."+id] = layer
		if p, ok := srcPos[srcPrefix]; ok {
			st.positions["modules."+id] = p
		}
		rows[idx] = row
	}
	dst["modules"] = rows
}

func copyModuleChildPositions(dst, src map[string]pos, from, to string) {
	for key, p := range src {
		if key == from {
			dst[to] = p
			continue
		}
		if strings.HasPrefix(key, from+".") {
			dst[to+strings.TrimPrefix(key, from)] = p
		}
	}
}

func copyModuleChildOrigins(dst Origins, from, to, layer string, src map[string]pos) {
	dst[to] = layer
	for key := range src {
		if strings.HasPrefix(key, from+".") {
			dst[to+strings.TrimPrefix(key, from)] = layer
		}
	}
}

func deletePrefix(m Origins, prefix string) {
	for key := range m {
		if key == prefix || strings.HasPrefix(key, prefix+".") {
			delete(m, key)
		}
	}
}

func deletePrefixPos(m map[string]pos, prefix string) {
	for key := range m {
		if key == prefix || strings.HasPrefix(key, prefix+".") {
			delete(m, key)
		}
	}
}

func replaceRoutes(st *mergeState, raw any, srcPos map[string]pos, layer string) {
	st.value["routes"] = raw
	st.origins["routes"] = layer
	deletePrefixPos(st.positions, "routes")
	for key, p := range srcPos {
		if strings.HasPrefix(key, "routes.") {
			st.positions[key] = p
		}
	}
}

func reorderModules(rows []any, newIDs []string) []any {
	for _, id := range newIDs {
		idx := -1
		for i, item := range rows {
			row, _ := asMap(item)
			if got, _ := row["id"].(string); got == id {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue
		}
		row, _ := asMap(rows[idx])
		targetID, direction := "", ""
		if after, ok := row["after"].(string); ok && after != "" {
			targetID, direction = after, "after"
		} else if before, ok := row["before"].(string); ok && before != "" {
			targetID, direction = before, "before"
		}
		if targetID == "" || targetID == id {
			continue
		}
		target := -1
		for i, item := range rows {
			other, _ := asMap(item)
			if got, _ := other["id"].(string); got == targetID {
				target = i
				break
			}
		}
		if target < 0 {
			continue
		}
		rows = append(rows[:idx], rows[idx+1:]...)
		if idx < target {
			target--
		}
		insert := target + 1
		if direction == "before" {
			insert = target
		}
		rows = append(rows[:insert], append([]any{row}, rows[insert:]...)...)
	}
	return rows
}
