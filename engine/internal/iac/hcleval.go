package iac

import (
	"fmt"
	"strconv"
	"strings"
)

// The expression half of the HCL reader, and the place almost every sentence
// in a report's "could not measure" list is written.
//
// THE RULE IT IS WRITTEN TO. Resolve what a parser can resolve, and for
// everything else say WHAT could not be resolved and WHY, naming the thing
// rather than the parser. "the variable database_version has no default, so
// its value is decided outside the configuration" tells a person what to go and
// look at. "unsupported expression" tells them this tool is bad.
//
// TWO THINGS IT DELIBERATELY DOES NOT DO.
//
// It evaluates NO FUNCTIONS. Not format, not join, not lower, not coalesce,
// not jsonencode. Every one of them is a few lines and every one of them is a
// chance to be subtly wrong in a way that reports a confident value that is
// not the value Terraform would produce, which is the one failure mode worse
// than reporting nothing. A call is named in the reason, so a person reading
// the report learns which function stood between them and the answer, and this
// package can grow one later against a measured case rather than against a
// guess.
//
// It never quotes the source of an expression into a reason. An expression can
// hold a credential written in line, and a reason is written into reports and
// logs. Reasons name shapes and addresses, never text from the file.

// cvalKind is what an evaluated expression turned out to be.
type cvalKind uint8

const (
	cvalScalar cvalKind = iota
	cvalList
	cvalObject
)

// cval is an evaluated expression: a scalar, a list, or an object, in one of
// the three states a Value has.
type cval struct {
	st   State
	why  string
	at   Position
	kind cvalKind
	s    string
	list []cval
	// keys keeps object key order, so that env variable names come out in the
	// order somebody wrote them rather than in map order.
	keys []string
	obj  map[string]cval
}

func unknownVal(at Position, format string, args ...any) cval {
	return cval{st: Unreadable, why: fmt.Sprintf(format, args...), at: at}
}

func scalar(s string, at Position) cval {
	return cval{st: Known, kind: cvalScalar, s: s, at: at}
}

// str turns an evaluated expression into a string Value, which is what almost
// every field of a Component is.
func (c cval) str() Value[string] {
	switch {
	case c.st == Known && c.kind == cvalScalar:
		return Resolved(c.s, c.at)
	case c.st == Known:
		// A list or an object where a string was expected. Reported as
		// unreadable rather than rendered, because rendering it would invent a
		// syntax nobody asked for.
		return Unresolved[string]("the value is a collection where this reader expected a single value", c.at)
	case c.st == Unreadable:
		return Unresolved[string](c.why, c.at)
	case c.st == Withheld:
		return Refused[string](c.why, c.at)
	default:
		return Value[string]{}
	}
}

// num turns an evaluated expression into an int Value.
func (c cval) num() Value[int] {
	switch {
	case c.st == Known && c.kind == cvalScalar:
		n, err := strconv.Atoi(strings.TrimSpace(c.s))
		if err != nil {
			if f, ferr := strconv.ParseFloat(strings.TrimSpace(c.s), 64); ferr == nil && f == float64(int(f)) {
				return Resolved(int(f), c.at)
			}
			return Unresolved[int]("the value is not a whole number", c.at)
		}
		return Resolved(n, c.at)
	case c.st == Unreadable:
		return Unresolved[int](c.why, c.at)
	case c.st == Withheld:
		return Refused[int](c.why, c.at)
	default:
		return Value[int]{}
	}
}

// evalCtx is what a file's expressions are resolved against: the root module's
// variables and locals, plus the workspace if the caller named one.
type evalCtx struct {
	vars      map[string]cval
	varDecl   map[string]bool // declared, whether or not it has a default
	sensitive map[string]bool // declared with sensitive = true
	locals    map[string]hclAttr
	workspace Value[string]
	// iterator is the dynamic block currently being read, if any. It is set
	// and restored around the body of a generated block rather than carried
	// through every call, because a dynamic block's iterator is in scope for
	// exactly that body and nowhere else.
	iterator string
	// resolving guards against a local that refers to itself, directly or
	// through others. Without it a cycle is a stack overflow, which takes the
	// whole process down rather than reporting an unreadable value.
	resolving map[string]bool
}

// enterBlock binds a dynamic block's iterator while its body is read, and
// returns the previous binding so the caller can restore it. Nesting is
// therefore correct: a dynamic block inside a dynamic block restores the outer
// iterator rather than clearing it.
func (e *evalCtx) enterBlock(blk hclBlock) string {
	prev := e.iterator
	if blk.iterator != "" {
		e.iterator = blk.iterator
	}
	return prev
}

func (e *evalCtx) eval(toks []token) cval {
	// Line breaks reach here from inside an interpolation or a wrapped
	// collection, where they are layout rather than part of the expression.
	toks = trimNewlines(toks)
	at := Position{}
	if len(toks) > 0 {
		at = toks[0].pos
	}
	if len(toks) == 0 {
		return cval{}
	}
	// A parenthesised expression is unwrapped so that `(var.x)` reads as
	// `var.x` rather than as an unreadable shape.
	for len(toks) >= 2 && isPunct(toks[0], "(") && matching(toks, 0) == len(toks)-1 {
		toks = toks[1 : len(toks)-1]
		if len(toks) == 0 {
			return unknownVal(at, "the expression is empty parentheses")
		}
	}
	if len(toks) == 1 {
		return e.evalSingle(toks[0])
	}
	switch {
	case isPunct(toks[0], "[") && matching(toks, 0) == len(toks)-1:
		return e.evalTuple(toks, at)
	case isPunct(toks[0], "{") && matching(toks, 0) == len(toks)-1:
		return e.evalObject(toks, at)
	case toks[0].kind == tokIdent && isPunct(toks[1], "(") && matching(toks, 1) == len(toks)-1:
		return unknownVal(at, "the expression calls %s, and this reader evaluates no functions, "+
			"so the value it would produce is decided when Terraform runs", toks[0].text)
	case toks[0].kind == tokIdent && (toks[0].text == "for" || toks[0].text == "if"):
		return unknownVal(at, "the expression is a for expression, whose result depends on what it "+
			"iterates over")
	}
	if trav, ok := traversalOf(toks); ok {
		return e.evalTraversal(trav, at)
	}
	if hasTopLevel(toks, "?") {
		return unknownVal(at, "the expression is a conditional, and which of its two branches "+
			"applies is decided when Terraform runs")
	}
	if op, ok := topLevelOperator(toks); ok {
		return unknownVal(at, "the expression combines values with %s, and this reader evaluates "+
			"no operators", op)
	}
	if hasTopLevel(toks, "*") {
		return unknownVal(at, "the expression is a splat over a collection this reader cannot "+
			"enumerate without running Terraform")
	}
	return unknownVal(at, "the expression is a shape this reader does not evaluate")
}

func (e *evalCtx) evalSingle(t token) cval {
	switch t.kind {
	case tokNumber:
		return scalar(t.text, t.pos)
	case tokString, tokHeredoc:
		return e.evalTemplate(t)
	case tokIdent:
		switch t.text {
		case "true", "false":
			return scalar(t.text, t.pos)
		case "null":
			// `null` is the configuration saying explicitly that this is not
			// set, which is Absent and not a value that could not be read.
			return cval{at: t.pos}
		}
		return e.evalTraversal([]string{t.text}, t.pos)
	}
	return unknownVal(t.pos, "the expression is a shape this reader does not evaluate")
}

// evalTemplate resolves a string or heredoc by resolving each interpolation in
// it. One unresolvable interpolation makes the whole string unresolvable,
// which is correct: half a string is not a value.
func (e *evalCtx) evalTemplate(t token) cval {
	var sb strings.Builder
	for _, part := range t.parts {
		switch {
		case part.directive:
			return unknownVal(part.pos, "the string contains a %%{ } template directive, and this "+
				"reader does not evaluate the template language's if and for")
		case part.interp != nil:
			inner := e.eval(part.interp)
			switch {
			case inner.st == Known && inner.kind == cvalScalar:
				sb.WriteString(inner.s)
			case inner.st == Unreadable:
				return cval{st: Unreadable, why: inner.why, at: inner.at}
			default:
				return unknownVal(part.pos, "an interpolation in the string produced no value")
			}
		default:
			sb.WriteString(part.lit)
		}
	}
	return scalar(sb.String(), t.pos)
}

// evalTuple evaluates a list, keeping each element's own state.
//
// An element this reader cannot resolve is kept AS an unreadable element
// rather than discarding the list, so that a list of five environment variable
// names with one computed name in it still yields four names and one honest
// hole. A reader that failed the whole list on one element would blank a
// feature for one surprising entry, which is the failure this repository has
// already been bitten by on a decode boundary.
func (e *evalCtx) evalTuple(toks []token, at Position) cval {
	inner := toks[1 : len(toks)-1]
	out := cval{st: Known, kind: cvalList, at: at}
	for _, elem := range splitTopLevel(inner, ",") {
		elem = trimNewlines(elem)
		if len(elem) == 0 {
			continue
		}
		out.list = append(out.list, e.eval(elem))
	}
	return out
}

// evalObject evaluates an object constructor, keeping key order.
func (e *evalCtx) evalObject(toks []token, at Position) cval {
	inner := toks[1 : len(toks)-1]
	out := cval{st: Known, kind: cvalObject, at: at, obj: map[string]cval{}}
	for _, entry := range splitObjectEntries(inner) {
		if len(entry) == 0 {
			continue
		}
		sep := -1
		depth := 0
		for i, t := range entry {
			if t.kind != tokPunct {
				continue
			}
			switch t.text {
			case "{", "[", "(":
				depth++
			case "}", "]", ")":
				depth--
			case "=", ":":
				if depth == 0 && sep < 0 {
					sep = i
				}
			}
		}
		if sep <= 0 {
			// An entry with no key is a shape this reader does not understand,
			// and a silently dropped entry would understate the object, so the
			// whole object becomes unreadable and says why.
			return unknownVal(at, "an entry in the object has no key this reader could read")
		}
		key, ok := objectKey(entry[:sep], e)
		if !ok {
			return unknownVal(entry[0].pos, "a key in the object is computed, so this reader "+
				"cannot say what the object contains")
		}
		if _, seen := out.obj[key]; !seen {
			out.keys = append(out.keys, key)
		}
		out.obj[key] = e.eval(entry[sep+1:])
	}
	return out
}

// objectKey reads an object's key, which HCL allows as a bare name or as a
// string.
func objectKey(toks []token, e *evalCtx) (string, bool) {
	if len(toks) == 1 {
		switch toks[0].kind {
		case tokIdent:
			return toks[0].text, true
		case tokString:
			if lit, ok := literalOf(toks[0]); ok {
				return lit, true
			}
			v := e.evalTemplate(toks[0])
			if v.st == Known {
				return v.s, true
			}
		}
	}
	if len(toks) >= 3 && isPunct(toks[0], "(") && matching(toks, 0) == len(toks)-1 {
		return objectKey(toks[1:len(toks)-1], e)
	}
	return "", false
}

// evalTraversal resolves a dotted reference, or says which one it could not
// resolve and why.
//
// The reasons here are the ones a person reads most often, so each names the
// exact thing standing between the reader and the value.
func (e *evalCtx) evalTraversal(parts []string, at Position) cval {
	if len(parts) == 0 {
		return unknownVal(at, "an empty reference")
	}
	root := parts[0]
	if e.iterator != "" && root == e.iterator {
		return unknownVal(at, "the value comes from the dynamic block's iterator, so it differs "+
			"for every instance the block generates and only a plan decides what those are")
	}
	switch root {
	case "var":
		if len(parts) < 2 {
			return unknownVal(at, "a reference to var with no variable named after it")
		}
		name := parts[1]
		if e.sensitive[name] {
			// The configuration itself said this holds a credential. Its
			// default is right there in the file and this reader has already
			// resolved it, and it is dropped here rather than filtered later,
			// because a value that is never put into a cval cannot be carried
			// into a report by some path nobody thought about.
			return unknownVal(at, "the variable %s is declared sensitive, so this reader does not "+
				"carry its value", name)
		}
		if v, ok := e.vars[name]; ok {
			if len(parts) > 2 {
				return e.index(v, parts[2:], at, "the variable "+name)
			}
			return v
		}
		if e.varDecl[name] {
			return unknownVal(at, "the variable %s has no default, so its value is decided "+
				"outside the configuration", name)
		}
		return unknownVal(at, "the variable %s is not declared in this root module, so its value "+
			"comes from somewhere this reader cannot see", name)
	case "local":
		if len(parts) < 2 {
			return unknownVal(at, "a reference to local with no name after it")
		}
		return e.evalLocal(parts[1], parts[2:], at)
	case "terraform":
		if len(parts) >= 2 && parts[1] == "workspace" {
			if ws, ok := e.workspace.Get(); ok {
				return scalar(ws, e.workspace.At())
			}
			return unknownVal(at, "the value depends on terraform.workspace, and no workspace was "+
				"named for this read")
		}
		return unknownVal(at, "the value depends on terraform.%s, which only Terraform itself knows",
			strings.Join(parts[1:], "."))
	case "each":
		return unknownVal(at, "the value depends on which instance of a for_each this is, which "+
			"only a plan decides")
	case "count":
		return unknownVal(at, "the value depends on count.index, which only a plan decides")
	case "self":
		return unknownVal(at, "the value refers to the resource's own attributes, which only "+
			"exist after an apply")
	case "path":
		return unknownVal(at, "the value depends on path.%s, which is decided by where Terraform "+
			"is run from", strings.Join(parts[1:], "."))
	case "data":
		return unknownVal(at, "the value comes from %s, a data source only a plan against the "+
			"cloud can resolve", strings.Join(parts[:min(3, len(parts))], "."))
	case "module":
		if len(parts) >= 2 {
			return unknownVal(at, "the value comes from module.%s, whose outputs are resolved when "+
				"Terraform runs", parts[1])
		}
		return unknownVal(at, "the value comes from a module output")
	}
	if len(parts) >= 3 {
		return unknownVal(at, "the value comes from %s.%s, an attribute that only exists after an "+
			"apply", parts[0], parts[1])
	}
	return unknownVal(at, "the reference %s is not one this reader can resolve", strings.Join(parts, "."))
}

// evalLocal resolves a local, guarding against a cycle.
func (e *evalCtx) evalLocal(name string, rest []string, at Position) cval {
	attr, ok := e.locals[name]
	if !ok {
		return unknownVal(at, "the local %s is not declared in this root module", name)
	}
	if e.resolving[name] {
		return unknownVal(at, "the local %s is defined in terms of itself, so this reader stopped "+
			"rather than following it round", name)
	}
	e.resolving[name] = true
	v := e.eval(attr.expr)
	delete(e.resolving, name)
	if len(rest) > 0 {
		return e.index(v, rest, at, "the local "+name)
	}
	return v
}

// index walks into a resolved collection, which is how `var.tags["env"]` and
// `local.sizes.small` resolve.
func (e *evalCtx) index(v cval, path []string, at Position, what string) cval {
	for _, step := range path {
		if v.st != Known || v.kind != cvalObject {
			if v.st == Unreadable {
				return v
			}
			return unknownVal(at, "%s is not an object, so this reader cannot read %s out of it",
				what, step)
		}
		next, ok := v.obj[step]
		if !ok {
			return cval{at: at}
		}
		v = next
	}
	return v
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// traversalOf reads a run of tokens as a dotted reference, and refuses
// anything with an index or a splat in it, which this reader does not follow.
func traversalOf(toks []token) ([]string, bool) {
	if len(toks) == 0 || toks[0].kind != tokIdent {
		return nil, false
	}
	out := []string{toks[0].text}
	for i := 1; i < len(toks); {
		if !isPunct(toks[i], ".") || i+1 >= len(toks) {
			return nil, false
		}
		nxt := toks[i+1]
		switch nxt.kind {
		case tokIdent:
			out = append(out, nxt.text)
		case tokNumber:
			out = append(out, nxt.text)
		case tokString:
			lit, ok := literalOf(nxt)
			if !ok {
				return nil, false
			}
			out = append(out, lit)
		default:
			return nil, false
		}
		i += 2
	}
	return out, true
}

func isPunct(t token, text string) bool { return t.kind == tokPunct && t.text == text }

// matching finds the index of the bracket closing the one at open, or minus
// one when it is never closed.
func matching(toks []token, open int) int {
	depth := 0
	for i := open; i < len(toks); i++ {
		if toks[i].kind != tokPunct {
			continue
		}
		switch toks[i].text {
		case "{", "[", "(":
			depth++
		case "}", "]", ")":
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitTopLevel splits on a separator that is not inside brackets. A line
// break also separates, because HCL lets an object be written over lines
// without commas.
func splitTopLevel(toks []token, sep string) [][]token {
	var out [][]token
	depth, start := 0, 0
	for i, t := range toks {
		if t.kind != tokPunct {
			continue
		}
		switch t.text {
		case "{", "[", "(":
			depth++
		case "}", "]", ")":
			depth--
		case sep:
			if depth == 0 {
				out = append(out, toks[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, toks[start:])
	return out
}

// trimNewlines drops the line breaks an author used as layout around an
// expression, so that `[\n  var.a,\n  var.b\n]` reads as two elements rather
// than as two elements wrapped in punctuation the classifier does not expect.
func trimNewlines(toks []token) []token {
	for len(toks) > 0 && toks[0].kind == tokNewline {
		toks = toks[1:]
	}
	for len(toks) > 0 && toks[len(toks)-1].kind == tokNewline {
		toks = toks[:len(toks)-1]
	}
	return toks
}

// splitObjectEntries splits an object constructor's body on BOTH separators
// HCL allows: the comma, and the line break. Real configurations use the line
// break far more often, and a reader that only knew the comma read a three
// entry object as one entry it could not understand.
func splitObjectEntries(toks []token) [][]token {
	var out [][]token
	depth, start := 0, 0
	flush := func(end int) {
		if entry := trimNewlines(toks[start:end]); len(entry) > 0 {
			out = append(out, entry)
		}
	}
	for i, t := range toks {
		if t.kind == tokNewline {
			if depth == 0 {
				flush(i)
				start = i + 1
			}
			continue
		}
		if t.kind != tokPunct {
			continue
		}
		switch t.text {
		case "{", "[", "(":
			depth++
		case "}", "]", ")":
			depth--
		case ",":
			if depth == 0 {
				flush(i)
				start = i + 1
			}
		}
	}
	flush(len(toks))
	return out
}

func hasTopLevel(toks []token, text string) bool {
	depth := 0
	for _, t := range toks {
		if t.kind != tokPunct {
			continue
		}
		switch t.text {
		case "{", "[", "(":
			depth++
		case "}", "]", ")":
			depth--
		case text:
			if depth == 0 {
				return true
			}
		}
	}
	return false
}

// operators are named in reasons, so that a person reading "combines values
// with ||" knows what to look at.
var operators = []string{"||", "&&", "==", "!=", "<=", ">=", "+", "-", "/", "%", "<", ">"}

func topLevelOperator(toks []token) (string, bool) {
	for _, op := range operators {
		if hasTopLevel(toks, op) {
			return op, true
		}
	}
	return "", false
}
