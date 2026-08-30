package oracle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// maxFindingsPerBody is how many differences one response contributes.
//
// A candidate that renamed a field on every element of a two hundred element
// array produces two hundred identical findings, and the two hundredth teaches
// nobody anything the first did not. The cap is announced in a note rather
// than applied silently, because a report that stops at forty without saying so
// is a report that looks complete.
const maxFindingsPerBody = 40

// differ walks two decoded documents together.
type differ struct {
	cfg      Config
	ignore   *matcher
	collect  *collector
	where    string
	order    int
	findings []Finding
	// truncated is set when the cap was reached, so the caller can say so.
	truncated bool
}

func (d *differ) add(f Finding) {
	if len(d.findings) >= maxFindingsPerBody {
		d.truncated = true
		return
	}
	f.SeverityName = f.Severity.String()
	f.Where = d.where
	f.order = d.order
	d.findings = append(d.findings, f)
}

// decodeJSON parses a body with numbers left as their literal text.
//
// UseNumber, not float64. Decoding into float64 turns 9007199254740993 into
// 9007199254740992 on both sides, so a bigint identifier that genuinely
// differs compares equal, and it also turns 1 and 1.0 into the same value
// before this package gets a chance to decide whether that matters. The
// literal is kept and the tolerance is applied deliberately.
func decodeJSON(body []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	// A second value in the stream is not JSON this package understands. It is
	// two documents, and comparing the first while ignoring the second would
	// report agreement about half a body.
	if dec.More() {
		return nil, fmt.Errorf("the body holds more than one JSON document")
	}
	return v, nil
}

// diffValue compares two decoded values at a path.
func (d *differ) diffValue(path []segment, base, cand any) {
	if d.ignore.matches(path) {
		return
	}

	bt, ct := typeName(base), typeName(cand)
	if bt != ct {
		f := newFinding(KindBodyType, Major, d.where, renderPath(path))
		f.Baseline, f.Candidate = render(base), render(cand)
		f.Detail = fmt.Sprintf("was %s, is %s", bt, ct)
		d.add(f)
		return
	}

	switch bv := base.(type) {
	case map[string]any:
		d.diffObject(path, bv, cand.(map[string]any))
	case []any:
		d.diffArray(path, bv, cand.([]any))
	default:
		if normaliseScalar(d.cfg, d.collect, renderPath(path), base, cand) {
			return
		}
		f := newFinding(KindBodyValue, Minor, d.where, renderPath(path))
		f.Baseline, f.Candidate = render(base), render(cand)
		f.Detail = numericHint(path, base, cand)
		d.add(f)
	}
}

// numericHint suggests an ignore rule when a number under a clock shaped name
// differs.
//
// This package refuses to treat a number as a timestamp, for the reason
// normalise.go gives. Refusing silently would leave somebody with a report full
// of epoch noise and no idea what to do about it, so the refusal comes with the
// line they would have to add. Advice, not behaviour: nothing here changes what
// was compared.
func numericHint(path []segment, base, cand any) string {
	if len(path) == 0 {
		return ""
	}
	last := path[len(path)-1]
	if last.isIndex {
		return ""
	}
	if _, ok := base.(json.Number); !ok {
		return ""
	}
	if _, ok := cand.(json.Number); !ok {
		return ""
	}
	if !clockShapedName(last.key) {
		return ""
	}
	return "numbers are never treated as clock readings; ignore this with " + renderPath(path)
}

// clockShapedName reports whether a field name reads like a time.
func clockShapedName(name string) bool {
	for _, suffix := range []string{"_at", "_time", "_ts", "_timestamp", "_epoch", "_ms", "_seconds"} {
		if len(name) > len(suffix) && name[len(name)-len(suffix):] == suffix {
			return true
		}
	}
	return name == "timestamp" || name == "time"
}

func (d *differ) diffObject(path []segment, base, cand map[string]any) {
	// Sorted, so two runs of this package produce the findings in the same
	// order. Ranging a map does not, and a report whose lines move between
	// runs cannot be diffed by anything, including a person.
	keys := make([]string, 0, len(base)+len(cand))
	seen := map[string]bool{}
	for k := range base {
		keys, seen[k] = append(keys, k), true
	}
	for k := range cand {
		if !seen[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	for _, k := range keys {
		child := childOf(path, keySegment(k))
		if d.ignore.matches(child) {
			continue
		}
		bv, inBase := base[k]
		cv, inCand := cand[k]
		switch {
		case inBase && inCand:
			d.diffValue(child, bv, cv)
		case inBase:
			// The candidate stopped returning it. Major, because a consumer
			// reading that field is now reading nothing, and nobody removes a
			// field from a response by accident less often than they add one.
			f := newFinding(KindBodyMissing, Major, d.where, renderPath(child))
			f.Baseline = render(bv)
			d.add(f)
		default:
			f := newFinding(KindBodyExtra, Minor, d.where, renderPath(child))
			f.Candidate = render(cv)
			d.add(f)
		}
	}
}

func (d *differ) diffArray(path []segment, base, cand []any) {
	if len(base) != len(cand) {
		// Directional. Fewer elements than the baseline is data the candidate
		// stopped returning, which is the shape of a filter that became too
		// strict. More elements is what a feature adds.
		sev := Minor
		detail := fmt.Sprintf("%d elements, was %d", len(cand), len(base))
		if len(cand) < len(base) {
			sev = Major
			detail = fmt.Sprintf("%d elements, was %d, so %s no longer returned",
				len(cand), len(base), plural(len(base)-len(cand), "element is", "elements are"))
		}
		f := newFinding(KindBodyLength, sev, d.where, renderPath(path))
		f.Baseline, f.Candidate = fmt.Sprint(len(base)), fmt.Sprint(len(cand))
		f.Detail = detail
		d.add(f)
		// Still compared element by element up to the shorter length. A list
		// that lost its last element and changed its first is two facts, and
		// reporting only the length would hide the one that matters more.
	}

	n := len(base)
	if len(cand) < n {
		n = len(cand)
	}

	// Reordering is checked before the elements, because a list whose members
	// are the same in a different order produces a difference at every index
	// and one useful sentence. The useful sentence is the one to print.
	if len(base) == len(cand) && n > 0 && d.reordered(base, cand) {
		f := newFinding(KindBodyOrder, Minor, d.where, renderPath(path))
		f.Detail = fmt.Sprintf("the same %d elements in a different order", n)
		d.add(f)
		return
	}

	for i := 0; i < n; i++ {
		d.diffValue(childOf(path, indexSegment(i)), base[i], cand[i])
	}
}

// reordered reports whether two arrays hold the same members in a different
// order.
//
// Membership is decided on the canonical form of each element, with the same
// normalisers applied, so a list of objects whose timestamps differ is still
// recognised as reordered rather than as entirely changed.
func (d *differ) reordered(base, cand []any) bool {
	if len(base) != len(cand) {
		return false
	}
	same := true
	for i := range base {
		if !equalValues(d.cfg, base[i], cand[i]) {
			same = false
			break
		}
	}
	if same {
		return false
	}
	bk := make([]string, len(base))
	ck := make([]string, len(cand))
	for i := range base {
		bk[i] = canonical(d.cfg, base[i])
		ck[i] = canonical(d.cfg, cand[i])
	}
	sort.Strings(bk)
	sort.Strings(ck)
	for i := range bk {
		if bk[i] != ck[i] {
			return false
		}
	}
	return true
}

// equalValues reports whether two decoded values agree under the
// normalisation, without recording anything.
//
// A separate walk from diffValue on purpose. This one answers a yes or no
// question for the reordering check, and running the recording walk to answer
// it would count normaliser hits for comparisons that were never reported.
func equalValues(cfg Config, a, b any) bool {
	if typeName(a) != typeName(b) {
		return false
	}
	switch av := a.(type) {
	case map[string]any:
		bv := b.(map[string]any)
		if len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			other, ok := bv[k]
			if !ok || !equalValues(cfg, v, other) {
				return false
			}
		}
		return true
	case []any:
		bv := b.([]any)
		if len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !equalValues(cfg, av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		return normaliseScalar(cfg, nil, "", a, b)
	}
}

// canonical renders a value with every normalised class collapsed to a token,
// so that two values the comparison would call equal render identically.
//
// Used only for the set comparison in the reordering check. It is not what a
// finding shows: a finding shows the real values, because the point of a
// finding is to be looked at.
func canonical(cfg Config, v any) string {
	switch tv := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(tv))
		for k := range tv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b []byte
		b = append(b, '{')
		for i, k := range keys {
			if i > 0 {
				b = append(b, ',')
			}
			b = append(b, k...)
			b = append(b, ':')
			b = append(b, canonical(cfg, tv[k])...)
		}
		return string(append(b, '}'))
	case []any:
		var b []byte
		b = append(b, '[')
		for i, e := range tv {
			if i > 0 {
				b = append(b, ',')
			}
			b = append(b, canonical(cfg, e)...)
		}
		return string(append(b, ']'))
	case string:
		if !cfg.KeepTimestamps && looksLikeTimestamp(tv) {
			return "<timestamp>"
		}
		if !cfg.KeepUUIDs && uuidPattern.MatchString(tv) {
			return "<uuid>"
		}
		return "s:" + tv
	case json.Number:
		// Through float64 and back, so 1 and 1.0 canonicalise the same way and
		// the set comparison agrees with normaliseScalar about them.
		if f, err := tv.Float64(); err == nil {
			return fmt.Sprintf("n:%g", f)
		}
		return "n:" + string(tv)
	default:
		return fmt.Sprintf("%v:%s", typeName(v), render(v))
	}
}
