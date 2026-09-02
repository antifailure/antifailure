package main

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/cli"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
)

// The sidecar writes a decision, the engine decodes it, and af net log shows
// it. Three structs, three json tag sets, and nothing until now made them
// agree. A field added to the first and forgotten in the other two is code
// that runs on every request, costs a line in the log, and answers nothing:
// the fact is recorded and then thrown away at the next boundary.
//
// It is a test rather than a review because a review is true on the day
// somebody does it. This finds the whole class on every run, and it fails when
// the next field is added rather than the next time somebody looks.
//
// The two exemption sets below are where a deliberate decision to not carry a
// field is written down. Adding a tag to one is how you say "on purpose", and
// the test then holds you to it: an exemption naming a field the sidecar no
// longer records fails too, so the list cannot rot into a way of hiding things.

// foldedIntoRequest are the parts af net log assembles into one request string
// rather than showing as separate keys, as "GET https://api.stripe.com/v1/charges".
// The fact reaches the reader; it just does not reach them under this name.
var foldedIntoRequest = map[string]string{
	"method": "shown inside the request string",
	"host":   "shown inside the request string",
	"port":   "shown inside the request string, unless it is the default for the scheme",
	"path":   "shown inside the request string",
	"tls":    "shown inside the request string, as the scheme",
}

// notADecision are the fields the sidecar puts on lines that are not a request
// at all: the ready line it prints once at startup, and the envelope every
// line carries. af net log shows decisions, so these have no place on one.
var notADecision = map[string]string{
	"event":       "names the kind of line; every decision has the same value",
	"env":         "the environment is what the reader asked about, so it is not repeated per row",
	"rules":       "on the ready line: how many rules loaded",
	"default":     "on the ready line: the default mode",
	"credentials": "on the ready line: how many sandbox values loaded",
}

// builtBySurface are keys af net log composes rather than reads, so the
// sidecar has nothing to record under that name.
var builtBySurface = map[string]string{
	"request": "assembled from method, tls, host, port and path",
}

// jsonTags is the set of names a struct actually serializes under.
func jsonTags(t reflect.Type) map[string]bool {
	tags := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}
		tags[name] = true
	}
	return tags
}

func TestEveryFactTheSidecarRecordsReachesASurface(t *testing.T) {
	recorded := jsonTags(reflect.TypeOf(record{}))
	decoded := jsonTags(reflect.TypeOf(local.Decision{}))
	shown := jsonTags(reflect.TypeOf(cli.DecisionJSON{}))

	// Collected and reported together rather than one require at a time,
	// because the value of a diff is the whole diff. A test that stops at the
	// first field turns one pass into as many passes as there are fields, and
	// somebody fixing them one at a time never sees that they are one problem.
	var gaps []string
	gap := func(format string, args ...any) { gaps = append(gaps, fmt.Sprintf(format, args...)) }

	for tag := range recorded {
		if _, ok := foldedIntoRequest[tag]; ok {
			continue
		}
		if _, ok := notADecision[tag]; ok {
			continue
		}
		if !decoded[tag] {
			gap("local.Decision has no field for %q, so the sidecar's value is discarded at the moment it is read", tag)
		}
		if !shown[tag] {
			gap("af net log -o json has no key for %q, so nothing a script reads can answer a question about it", tag)
		}
	}

	for tag := range shown {
		if _, ok := builtBySurface[tag]; ok {
			continue
		}
		if !recorded[tag] {
			gap("af net log -o json publishes %q and the sidecar never records it, so the key is either always empty or filled from somewhere the log does not say", tag)
		}
	}

	// An exemption for a field that no longer exists is an exemption that has
	// stopped meaning anything, and the next reader would trust it.
	for _, set := range []map[string]string{foldedIntoRequest, notADecision} {
		for tag := range set {
			if !recorded[tag] {
				gap("%q is exempted from reaching a surface and the sidecar does not record it; delete the exemption", tag)
			}
		}
	}
	for tag := range builtBySurface {
		if !shown[tag] {
			gap("%q is exempted as composed by af net log, which no longer publishes it", tag)
		}
	}

	sort.Strings(gaps)
	require.Empty(t, gaps, "the sidecar spends a line per request on facts no surface can show:\n\t%s",
		strings.Join(gaps, "\n\t"))
}
