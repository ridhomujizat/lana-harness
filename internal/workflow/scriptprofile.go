package workflow

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Profile thresholds (REQ-DG-002). Every number the signals compare against is a
// named constant so a behaviour change is a change of a named value.
const (
	// profileZeroBaselineMax is the most a number field may be 0 or null in a
	// baseline output for zeros to treat that output as never zero.
	profileZeroBaselineMax = 0.2
	// profileZeroCurrentMin is the least a number field must be 0 or null in the
	// current output for zeros to fire.
	profileZeroCurrentMin = 0.8
	// profileMinValues is the fewest current values most signals need.
	profileMinValues = 3
	// profileZeroBaselineMinOutputs is the fewest baseline outputs for the
	// top-level number zeros rule.
	profileZeroBaselineMinOutputs = 3
	// profileConstantDistinctMin is the least distinct ratio a baseline output
	// must show for the constant signal to treat that output as varied.
	profileConstantDistinctMin = 0.5
	// profileConstantShareMin is the least most-common value share in the current
	// output for the constant signal to fire.
	profileConstantShareMin = 0.8
	// profileLengthMinBaseline is the shortest baseline mean rune length for the
	// length signal to apply.
	profileLengthMinBaseline = 8
	// profileLengthLowRatio is the smallest current/baseline mean length ratio
	// that does not fire the length signal.
	profileLengthLowRatio = 0.25
	// profileLengthHighRatio is the largest current/baseline mean length ratio
	// that does not fire the length signal.
	profileLengthHighRatio = 4
	// profileCopyMinLen is the fewest runes a contained b value needs for copy.
	profileCopyMinLen = 8
	// profileCopyCurrentMin is the least current containment share for copy.
	profileCopyCurrentMin = 0.8
	// profileCopyBaselineMax is the most containment share of one baseline output
	// for copy to treat that output as not already copying.
	profileCopyBaselineMax = 0.2
	// profileShapeDominantMin is the least share of the dominant class a baseline
	// output must show for the shape signal to treat that output as normal.
	profileShapeDominantMin = 0.9
	// profileShapeCurrentMax is the current share of the dominant class at or
	// above which shape does not fire.
	profileShapeCurrentMax = 0.5
	// profileBlankBaselineMax is the most empty share of one baseline output for
	// blank to treat that output as not already blank.
	profileBlankBaselineMax = 0.2
	// profileBlankCurrentMin is the least current empty share for blank.
	profileBlankCurrentMin = 0.8
	// profileCountMinMedian is the least baseline median length for count.
	profileCountMinMedian = 5
	// profileCountLowRatio is the current/baseline median ratio at or above which
	// the count signal does not fire.
	profileCountLowRatio = 0.3
)

// profileKind names the field path shapes the profile signals work on.
const (
	profileKindTopString  = "top-string"
	profileKindTopNumber  = "top-number"
	profileKindItemString = "item-string"
	profileKindItemNumber = "item-number"
	profileKindItemArray  = "item-array"
)

// profileClass names the string class of the shape signal (REQ-DG-002).
type profileClass string

const (
	profileClassURL    profileClass = "url"
	profileClassNumber profileClass = "number"
	profileClassText   profileClass = "text"
)

var (
	// profileURLRe matches the url class of the shape signal (REQ-DG-002).
	profileURLRe = regexp.MustCompile(`^https?://\S+$`)
	// profileNumberRe matches the number class of the shape signal.
	profileNumberRe = regexp.MustCompile(`^[-+]?[\d.,]+%?$`)
)

// profileField is one field path the profile checks: a top-level string or number
// field, an item array (count only), or an item field of such an array.
type profileField struct {
	path       string
	kind       string
	array      string
	item       string
	allowEmpty bool
	siblings   []string
}

// CheckProfile reports the first strong profile drift of out against baseline
// (REQ-DG-001, REQ-DG-002). A share-based signal fires on the per-output baseline
// vote of REQ-DG-011. It returns nil when there are fewer than two baseline
// outputs. params carries output_schema, item_schema and allow_empty in the
// shapes CheckContract reads. The signals run over every field path in sorted
// order and the first hit wins.
func CheckProfile(params map[string]any, baseline []any, out any) *DriftError {
	if len(baseline) < 2 {
		return nil
	}
	for _, fp := range profileFields(params) {
		if de := profileCheckField(fp, baseline, out); de != nil {
			return de
		}
	}
	return nil
}

// profileFields lists the field paths of a script_parse node in sorted order: a
// top-level string or number field, an item array (count only) and its item
// fields (REQ-DG-002). An array field that is not an item_schema key gets no
// signals.
func profileFields(params map[string]any) []profileField {
	schema := asMapParam(params["output_schema"])
	itemSchema := asMapParam(params["item_schema"])
	allow := allowEmptyNames(params["allow_empty"])
	var fields []profileField
	for _, f := range sortedKeys(schema) {
		spec, ok := schema[f].(string)
		if !ok {
			continue
		}
		typ := schemaFieldType(spec)
		if entry := asMapParam(itemSchema[f]); entry != nil && profileIsArrayType(typ) {
			fields = append(fields, profileField{path: f, kind: profileKindItemArray, array: f})
			siblings := profileStringFields(entry)
			for _, k := range sortedKeys(entry) {
				kspec, ok := entry[k].(string)
				if !ok {
					continue
				}
				kind := ""
				switch schemaFieldType(kspec) {
				case "string":
					kind = profileKindItemString
				case "integer", "number":
					kind = profileKindItemNumber
				}
				if kind == "" {
					continue
				}
				fields = append(fields, profileField{
					path:       f + "." + k,
					kind:       kind,
					array:      f,
					item:       k,
					allowEmpty: allow[f+"."+k],
					siblings:   siblings,
				})
			}
			continue
		}
		if profileIsArrayType(typ) {
			continue
		}
		switch typ {
		case "string":
			fields = append(fields, profileField{path: f, kind: profileKindTopString, allowEmpty: allow[f]})
		case "integer", "number":
			fields = append(fields, profileField{path: f, kind: profileKindTopNumber})
		}
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].path < fields[j].path })
	return fields
}

// profileStringFields returns the sorted names of the string item fields of one
// item_schema entry, the candidate b values of the copy signal.
func profileStringFields(entry map[string]any) []string {
	var out []string
	for _, k := range sortedKeys(entry) {
		if spec, ok := entry[k].(string); ok && schemaFieldType(spec) == "string" {
			out = append(out, k)
		}
	}
	return out
}

// profileIsArrayType reports whether a schema type name is an array.
func profileIsArrayType(typ string) bool {
	return typ == "array" || strings.HasSuffix(typ, "[]")
}

// profileCheckField runs the REQ-DG-002 signals for one field path in order and
// returns the first drift.
func profileCheckField(fp profileField, baseline []any, out any) *DriftError {
	if de := profileCheckZeros(fp, baseline, out); de != nil {
		return de
	}
	if de := profileCheckConstant(fp, baseline, out); de != nil {
		return de
	}
	if de := profileCheckLength(fp, baseline, out); de != nil {
		return de
	}
	if de := profileCheckCopy(fp, baseline, out); de != nil {
		return de
	}
	if de := profileCheckShape(fp, baseline, out); de != nil {
		return de
	}
	if de := profileCheckBlank(fp, baseline, out); de != nil {
		return de
	}
	return profileCheckCount(fp, baseline, out)
}

// profileVote is the baseline vote of a share-based signal (REQ-DG-011): the
// number of baseline outputs with enough values to vote and how many of those
// are normal for the signal.
type profileVote struct {
	voters int
	normal int
}

// profileVoteFires reports whether a signal with this vote fires (REQ-DG-011):
// fewer than two voters is skipped, and the normal voters must be a strict
// majority of the voters.
func profileVoteFires(v profileVote) bool {
	return v.voters >= 2 && v.normal*2 > v.voters
}

// profileVoterMin is the fewest values a baseline output needs to vote for a
// signal (REQ-DG-011): one for a top-level string, which counts as one value,
// and profileMinValues for an item field.
func profileVoterMin(fp profileField) int {
	if fp.kind == profileKindTopString {
		return 1
	}
	return profileMinValues
}

// profileCheckZeros applies the zeros signal (REQ-DG-002, REQ-DG-011): a number
// field that was never 0 in most baseline outputs becomes mostly 0. An item
// number votes per baseline output with at least profileMinValues values and is
// normal when its own share of 0-or-null is at most profileZeroBaselineMax; a
// top-level number fires when the current value is 0 and more than half of at
// least profileZeroBaselineMinOutputs baseline outputs are non-zero.
func profileCheckZeros(fp profileField, baseline []any, out any) *DriftError {
	switch fp.kind {
	case profileKindItemNumber:
		cur := profileItemValues([]any{out}, fp.array, fp.item)
		if len(cur) < profileMinValues {
			return nil
		}
		curZero := profileCountZeroOrNull(cur)
		if float64(curZero)/float64(len(cur)) < profileZeroCurrentMin {
			return nil
		}
		var v profileVote
		for _, vals := range profileItemPerOutput(baseline, fp.array, fp.item) {
			if len(vals) < profileMinValues {
				continue
			}
			v.voters++
			if float64(profileCountZeroOrNull(vals))/float64(len(vals)) <= profileZeroBaselineMax {
				v.normal++
			}
		}
		if !profileVoteFires(v) {
			return nil
		}
		return profileDrift(fp.path, "zeros", fmt.Sprintf("%d/%d values are 0, baseline normal in %d/%d runs",
			curZero, len(cur), v.normal, v.voters))
	case profileKindTopNumber:
		cur, ok := profileTopNumber(out, fp.path)
		if !ok || cur != 0 {
			return nil
		}
		if len(baseline) < profileZeroBaselineMinOutputs {
			return nil
		}
		nonZero := 0
		for _, o := range baseline {
			if v, ok := profileTopNumber(o, fp.path); ok && v != 0 {
				nonZero++
			}
		}
		if nonZero*2 <= len(baseline) {
			return nil
		}
		return profileDrift(fp.path, "zeros", fmt.Sprintf("1/1 values are 0, baseline non-zero in %d/%d runs",
			nonZero, len(baseline)))
	}
	return nil
}

// profileCheckConstant applies the constant signal (REQ-DG-002, REQ-DG-011): a
// string field whose baseline outputs are each varied becomes mostly one value in
// the current output. A baseline output votes with at least profileVoterMin values
// and is normal when it has at least profileMinValues non-empty values with a
// distinct ratio of at least profileConstantDistinctMin.
func profileCheckConstant(fp profileField, baseline []any, out any) *DriftError {
	if fp.kind != profileKindItemString && fp.kind != profileKindTopString {
		return nil
	}
	min := profileVoterMin(fp)
	var v profileVote
	for _, vals := range profileStringPerOutput(fp, baseline) {
		if len(vals) < min {
			continue
		}
		v.voters++
		if len(vals) >= profileMinValues && profileDistinctRatio(vals) >= profileConstantDistinctMin {
			v.normal++
		}
	}
	cur := profileStringValues(fp, out)
	if len(cur) < profileMinValues {
		return nil
	}
	_, cnt := profileMostCommon(cur)
	if float64(cnt)/float64(len(cur)) < profileConstantShareMin {
		return nil
	}
	if !profileVoteFires(v) {
		return nil
	}
	return profileDrift(fp.path, "constant", fmt.Sprintf("%d/%d values are the same, baseline normal in %d/%d runs",
		cnt, len(cur), v.normal, v.voters))
}

// profileCheckLength applies the length signal (REQ-DG-002, REQ-DG-011): a string
// field whose baseline median per-output mean rune length is at least
// profileLengthMinBaseline drifts when the current mean falls below
// profileLengthLowRatio or rises above profileLengthHighRatio of the reference.
// The reference is the median of the mean rune lengths of the baseline outputs
// that have non-empty values. A top-level string counts as one value, so it can
// fire with one current value; an item field needs profileMinValues current
// values.
func profileCheckLength(fp profileField, baseline []any, out any) *DriftError {
	if fp.kind != profileKindItemString && fp.kind != profileKindTopString {
		return nil
	}
	var means []float64
	for _, vals := range profileStringPerOutput(fp, baseline) {
		if len(vals) == 0 {
			continue
		}
		means = append(means, profileMeanRunes(vals))
	}
	if len(means) == 0 {
		return nil
	}
	ref := profileMedianFloat(means)
	if ref < profileLengthMinBaseline {
		return nil
	}
	cur := profileStringValues(fp, out)
	if len(cur) == 0 {
		return nil
	}
	if fp.kind == profileKindItemString && len(cur) < profileMinValues {
		return nil
	}
	curMean := profileMeanRunes(cur)
	ratio := curMean / ref
	if ratio >= profileLengthLowRatio && ratio <= profileLengthHighRatio {
		return nil
	}
	return profileDrift(fp.path, "length", fmt.Sprintf("mean %g, baseline %g", curMean, ref))
}

// profileCheckCopy applies the copy signal (REQ-DG-002, REQ-DG-011): a string item
// field a whose elements now contain the value of another string item field b in
// the same array (b at least profileCopyMinLen runes). Candidates b are checked in
// sorted order and the drift is reported on path a. A baseline output votes with
// at least profileMinValues elements with a non-empty a and is normal when its own
// containment share is at most profileCopyBaselineMax.
func profileCheckCopy(fp profileField, baseline []any, out any) *DriftError {
	if fp.kind != profileKindItemString {
		return nil
	}
	curElems := profileItemElements([]any{out}, fp.array)
	for _, b := range fp.siblings {
		if b == fp.item {
			continue
		}
		curShare, curDenom, curHit := profileCopyShare(curElems, fp.item, b)
		if curDenom < profileMinValues || curShare < profileCopyCurrentMin {
			continue
		}
		var v profileVote
		for _, o := range baseline {
			share, denom, _ := profileCopyShare(profileItemElements([]any{o}, fp.array), fp.item, b)
			if denom < profileMinValues {
				continue
			}
			v.voters++
			if share <= profileCopyBaselineMax {
				v.normal++
			}
		}
		if !profileVoteFires(v) {
			continue
		}
		return profileDrift(fp.path, "copy", fmt.Sprintf("%d/%d contain %q, baseline normal in %d/%d runs",
			curHit, curDenom, fp.array+"."+b, v.normal, v.voters))
	}
	return nil
}

// profileCheckShape applies the shape signal (REQ-DG-002, REQ-DG-011): a string
// field whose values are mostly one class (url, number, text) drifts when the
// current share of that class falls below profileShapeCurrentMax. The dominant
// class is the one most baseline voters show, ties resolved in the class order
// url, number, text; a voter is normal when its own share of that class is at
// least profileShapeDominantMin. A top-level string may fire with one current
// value.
func profileCheckShape(fp profileField, baseline []any, out any) *DriftError {
	if fp.kind != profileKindItemString && fp.kind != profileKindTopString {
		return nil
	}
	min := profileVoterMin(fp)
	per := profileStringPerOutput(fp, baseline)
	var votes []profileClass
	for _, vals := range per {
		if len(vals) < min {
			continue
		}
		dom, _ := profileDominantClass(profileClassCounts(vals), len(vals))
		votes = append(votes, dom)
	}
	dom := profileMajorityClass(votes)
	cur := profileStringValues(fp, out)
	if len(cur) == 0 {
		return nil
	}
	if fp.kind == profileKindItemString && len(cur) < profileMinValues {
		return nil
	}
	curCounts := profileClassCounts(cur)
	curShare := float64(curCounts[dom]) / float64(len(cur))
	if curShare >= profileShapeCurrentMax {
		return nil
	}
	var v profileVote
	for _, vals := range per {
		if len(vals) < min {
			continue
		}
		v.voters++
		if float64(profileClassCounts(vals)[dom])/float64(len(vals)) >= profileShapeDominantMin {
			v.normal++
		}
	}
	if !profileVoteFires(v) {
		return nil
	}
	return profileDrift(fp.path, "shape", fmt.Sprintf("%s share %d/%d, baseline normal in %d/%d runs",
		dom, curCounts[dom], len(cur), v.normal, v.voters))
}

// profileCheckBlank applies the blank signal (REQ-DG-002, REQ-DG-011): an
// allow_empty string field whose baseline is mostly non-empty becomes mostly
// empty. Only fields named in allow_empty reach it. A baseline output votes with
// at least profileVoterMin values and is normal when its own empty share is at
// most profileBlankBaselineMax.
func profileCheckBlank(fp profileField, baseline []any, out any) *DriftError {
	if !fp.allowEmpty {
		return nil
	}
	if fp.kind != profileKindItemString && fp.kind != profileKindTopString {
		return nil
	}
	min := profileVoterMin(fp)
	var v profileVote
	for _, o := range baseline {
		empty, total := profileBlankCounts(fp, []any{o})
		if total < min {
			continue
		}
		v.voters++
		if float64(empty)/float64(total) <= profileBlankBaselineMax {
			v.normal++
		}
	}
	curEmpty, curTotal := profileBlankCounts(fp, []any{out})
	if curTotal < profileMinValues {
		return nil
	}
	if float64(curEmpty)/float64(curTotal) < profileBlankCurrentMin {
		return nil
	}
	if !profileVoteFires(v) {
		return nil
	}
	return profileDrift(fp.path, "blank", fmt.Sprintf("%d/%d empty, baseline normal in %d/%d runs",
		curEmpty, curTotal, v.normal, v.voters))
}

// profileCheckCount applies the count signal (REQ-DG-002): an item array whose
// baseline median length is at least profileCountMinMedian shrinks below
// profileCountLowRatio of it in the current output.
func profileCheckCount(fp profileField, baseline []any, out any) *DriftError {
	if fp.kind != profileKindItemArray {
		return nil
	}
	median := profileMedian(profileArrayLens(baseline, fp.array))
	if median < profileCountMinMedian {
		return nil
	}
	cur := profileArrayLen(out, fp.array)
	if float64(cur) >= profileCountLowRatio*median {
		return nil
	}
	return profileDrift(fp.path, "count", fmt.Sprintf("%d items, baseline median %g", cur, median))
}

// profileDrift builds a profile DriftError with the shared message format
// "field \"path\": signal: detail" (REQ-DG-002).
func profileDrift(path, signal, detail string) *DriftError {
	return &DriftError{
		Code:    DriftProfile,
		Message: fmt.Sprintf("field %q: %s: %s", path, signal, detail),
	}
}

// profileItemValues returns every value of item field item of array field array,
// aggregated over outputs, with a missing element or field as nil.
func profileItemValues(outputs []any, array, item string) []any {
	var vals []any
	for _, per := range profileItemPerOutput(outputs, array, item) {
		vals = append(vals, per...)
	}
	return vals
}

// profileItemPerOutput returns, per output, the values of item field item of
// array field array, with a non-object element as nil.
func profileItemPerOutput(outputs []any, array, item string) [][]any {
	res := make([][]any, 0, len(outputs))
	for _, o := range outputs {
		m, ok := normalize(o).(map[string]any)
		if !ok {
			res = append(res, nil)
			continue
		}
		arr, ok := normalize(m[array]).([]any)
		if !ok {
			res = append(res, nil)
			continue
		}
		vals := make([]any, 0, len(arr))
		for _, e := range arr {
			em, ok := normalize(e).(map[string]any)
			if !ok {
				vals = append(vals, nil)
				continue
			}
			vals = append(vals, em[item])
		}
		res = append(res, vals)
	}
	return res
}

// profileCountZeroOrNull counts values that are 0 or null (a number field's zero
// signal numerator).
func profileCountZeroOrNull(vals []any) int {
	n := 0
	for _, v := range vals {
		if v == nil {
			n++
			continue
		}
		if f, ok := asFloat(v); ok && f == 0 {
			n++
		}
	}
	return n
}

// profileTopNumber reads a top-level number field from one output; ok is false
// when the field is absent or null.
func profileTopNumber(o any, field string) (float64, bool) {
	m, ok := normalize(o).(map[string]any)
	if !ok {
		return 0, false
	}
	if v, present := m[field]; present && v != nil {
		return asFloat(v)
	}
	return 0, false
}

// profileStringPerOutput returns, per output, the non-empty trimmed string values
// of the field: one value for a top-level string, the item values of the array
// for an item field.
func profileStringPerOutput(fp profileField, outputs []any) [][]string {
	res := make([][]string, 0, len(outputs))
	for _, o := range outputs {
		res = append(res, profileStringValues(fp, o))
	}
	return res
}

// profileStringValues returns the non-empty trimmed string values of one field in
// one output.
func profileStringValues(fp profileField, o any) []string {
	m, ok := normalize(o).(map[string]any)
	if !ok {
		return nil
	}
	switch fp.kind {
	case profileKindTopString:
		if s, ok := m[fp.path].(string); ok {
			if t := strings.TrimSpace(s); t != "" {
				return []string{t}
			}
		}
	case profileKindItemString:
		arr, ok := normalize(m[fp.array]).([]any)
		if !ok {
			return nil
		}
		var vals []string
		for _, e := range arr {
			em, ok := normalize(e).(map[string]any)
			if !ok {
				continue
			}
			if s, ok := em[fp.item].(string); ok {
				if t := strings.TrimSpace(s); t != "" {
					vals = append(vals, t)
				}
			}
		}
		return vals
	}
	return nil
}

// profileDistinctRatio is the distinct non-empty value count over the value count.
func profileDistinctRatio(vals []string) float64 {
	if len(vals) == 0 {
		return 0
	}
	seen := make(map[string]bool, len(vals))
	for _, v := range vals {
		seen[v] = true
	}
	return float64(len(seen)) / float64(len(vals))
}

// profileMostCommon returns the most common value and its count.
func profileMostCommon(vals []string) (string, int) {
	counts := make(map[string]int, len(vals))
	best := ""
	bestN := 0
	for _, v := range vals {
		counts[v]++
		if counts[v] > bestN {
			best = v
			bestN = counts[v]
		}
	}
	return best, bestN
}

// profileMeanRunes is the mean rune length of the values.
func profileMeanRunes(vals []string) float64 {
	if len(vals) == 0 {
		return 0
	}
	total := 0
	for _, v := range vals {
		total += utf8.RuneCountInString(v)
	}
	return float64(total) / float64(len(vals))
}

// profileItemElements returns every element of the array field across outputs.
func profileItemElements(outputs []any, field string) []any {
	var elems []any
	for _, o := range outputs {
		m, ok := normalize(o).(map[string]any)
		if !ok {
			continue
		}
		arr, ok := normalize(m[field]).([]any)
		if !ok {
			continue
		}
		elems = append(elems, arr...)
	}
	return elems
}

// profileCopyShare returns the share of elements where field a is non-empty and
// contains the trimmed value of field b (at least profileCopyMinLen runes), the
// number with a non-empty a, and the number that also contain b.
func profileCopyShare(elems []any, a, b string) (float64, int, int) {
	denom, hit := 0, 0
	for _, e := range elems {
		em, ok := normalize(e).(map[string]any)
		if !ok {
			continue
		}
		av, ok := profileNonEmptyString(em[a])
		if !ok {
			continue
		}
		denom++
		bv, ok := profileNonEmptyString(em[b])
		if !ok || utf8.RuneCountInString(bv) < profileCopyMinLen {
			continue
		}
		if strings.Contains(av, bv) {
			hit++
		}
	}
	if denom == 0 {
		return 0, 0, 0
	}
	return float64(hit) / float64(denom), denom, hit
}

// profileNonEmptyString trims a string value and reports whether it is non-empty.
func profileNonEmptyString(v any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	t := strings.TrimSpace(s)
	return t, t != ""
}

// profileBlankCounts returns the empty and total value counts of an allow_empty
// string field: a top-level field counts its present strings, an item field
// counts every element with an absent or null value as empty.
func profileBlankCounts(fp profileField, outputs []any) (int, int) {
	empty, total := 0, 0
	switch fp.kind {
	case profileKindTopString:
		for _, o := range outputs {
			m, ok := normalize(o).(map[string]any)
			if !ok {
				continue
			}
			s, ok := m[fp.path].(string)
			if !ok {
				continue
			}
			total++
			if strings.TrimSpace(s) == "" {
				empty++
			}
		}
	case profileKindItemString:
		for _, o := range outputs {
			m, ok := normalize(o).(map[string]any)
			if !ok {
				continue
			}
			arr, ok := normalize(m[fp.array]).([]any)
			if !ok {
				continue
			}
			for _, e := range arr {
				total++
				em, ok := normalize(e).(map[string]any)
				if !ok {
					empty++
					continue
				}
				s, ok := em[fp.item].(string)
				if !ok || strings.TrimSpace(s) == "" {
					empty++
				}
			}
		}
	}
	return empty, total
}

// profileArrayLens returns the length of the array field in each output.
func profileArrayLens(outputs []any, field string) []int {
	lens := make([]int, 0, len(outputs))
	for _, o := range outputs {
		lens = append(lens, profileArrayLen(o, field))
	}
	return lens
}

// profileArrayLen returns the length of the array field in one output, 0 when it
// is absent.
func profileArrayLen(o any, field string) int {
	m, ok := normalize(o).(map[string]any)
	if !ok {
		return 0
	}
	arr, ok := normalize(m[field]).([]any)
	if !ok {
		return 0
	}
	return len(arr)
}

// profileValueClass names the shape class of one non-empty string value.
func profileValueClass(s string) profileClass {
	switch {
	case profileURLRe.MatchString(s):
		return profileClassURL
	case profileNumberRe.MatchString(s):
		return profileClassNumber
	default:
		return profileClassText
	}
}

// profileClassCounts counts the values of each shape class.
func profileClassCounts(vals []string) map[profileClass]int {
	counts := map[profileClass]int{}
	for _, v := range vals {
		counts[profileValueClass(v)]++
	}
	return counts
}

// profileDominantClass returns the class with the largest share over total.
func profileDominantClass(counts map[profileClass]int, total int) (profileClass, float64) {
	best := profileClassText
	bestN := 0
	for _, c := range []profileClass{profileClassURL, profileClassNumber, profileClassText} {
		if counts[c] > bestN {
			best = c
			bestN = counts[c]
		}
	}
	if total == 0 {
		return best, 0
	}
	return best, float64(bestN) / float64(total)
}

// profileMajorityClass returns the class most baseline voters show, ties resolved
// in the class order url, number, text (REQ-DG-011).
func profileMajorityClass(votes []profileClass) profileClass {
	tally := map[profileClass]int{}
	for _, c := range votes {
		tally[c]++
	}
	best := profileClassText
	bestN := 0
	for _, c := range []profileClass{profileClassURL, profileClassNumber, profileClassText} {
		if tally[c] > bestN {
			best = c
			bestN = tally[c]
		}
	}
	return best
}

// profileMedian returns the median of the lengths.
func profileMedian(vals []int) float64 {
	if len(vals) == 0 {
		return 0
	}
	s := append([]int(nil), vals...)
	sort.Ints(s)
	n := len(s)
	if n%2 == 1 {
		return float64(s[n/2])
	}
	return float64(s[n/2-1]+s[n/2]) / 2
}

// profileMedianFloat returns the median of the values, 0 when empty.
func profileMedianFloat(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}
