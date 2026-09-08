// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package aurora

// The engine's error codes, reconstructed from outside the engine module.
//
// Three of the twenty four database conformance behaviours require a specific
// catalog code: branching an unverified golden must be AF-MSK-001, branching a
// missing one AF-DB-004, and passing the branch limit AF-DB-006. The suite
// checks them with errors.Is against errors built by
// engine/internal/errors.Coded.
//
// That package is INTERNAL. Go refuses the import by path from
// github.com/antifailure/antifailure/ee/engine, so a provider written outside
// engine/ cannot construct one of those errors, and there is no exported
// constructor anywhere in engine/pkg. This is a real gap in the extension
// surface rather than a preference: tools/socketcheck reads signatures and a
// signature is not where this lives, so an out of module provider compiles,
// looks correct, and fails three conformance behaviours it has no way to pass.
//
// What makes a way through is that errors.Is consults the ERROR's own Is
// method against the target, not the target's against the error. So this type
// recognises the target by the code its message begins with. It is a
// workaround and it is written as one: if engine/pkg ever exports a code
// constructor, every use of this file should become a call to it.
//
// The recognition is by the first AF-XXX-000 shaped token in the rendered
// target, because engine/internal/errors renders "AF-DB-004: the golden ..."
// and prefixes an operation when one is set. Matching the first token rather
// than searching the whole string is what stops a catalog message that
// happened to mention another code from matching the wrong one.

import (
	"regexp"
	"strings"
)

// codePattern is the shape of a catalog code, which is stable: two to four
// upper case letters for the area, three digits for the entry.
var codePattern = regexp.MustCompile(`AF-[A-Z]{2,4}-[0-9]{3}`)

// The three codes this provider has to be able to produce, plus the two it
// uses for its own refusals.
const (
	codeUnverifiedGolden = "AF-MSK-001"
	codeNoSuchGolden     = "AF-DB-004"
	codeGoldenReferenced = "AF-DB-005"
	codeBranchLimit      = "AF-DB-006"
)

// codedError is an error carrying a catalog code.
type codedError struct {
	code string
	// message is the cause sentence. It is rendered after the code, in the
	// same order engine/internal/errors renders one, so a person reading a
	// terminal cannot tell which side of the module boundary it came from.
	message string
	// err is the wrapped cause, if any.
	err error
}

func coded(code, message string) *codedError {
	return &codedError{code: code, message: message}
}

func (e *codedError) Error() string {
	var b strings.Builder
	b.WriteString(e.code)
	b.WriteString(": ")
	b.WriteString(e.message)
	if e.err != nil {
		b.WriteString(": ")
		b.WriteString(e.err.Error())
	}
	return b.String()
}

func (e *codedError) Unwrap() error { return e.err }

// Is reports whether target carries the same catalog code.
//
// Asked of the target's rendered text because the target is a type this
// module cannot name. The comment at the top of this file says why that is
// the only door open.
func (e *codedError) Is(target error) bool {
	if target == nil {
		return false
	}
	if other, ok := target.(*codedError); ok {
		return other.code == e.code
	}
	return codeOf(target.Error()) == e.code
}

// codeOf returns the first catalog code in a rendered error, or the empty
// string.
func codeOf(rendered string) string {
	return codePattern.FindString(rendered)
}
