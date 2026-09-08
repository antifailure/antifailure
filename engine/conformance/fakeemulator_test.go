package conformance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/antifailure/antifailure/engine/pkg/livekey"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The broken emulator, which exists so that RunEmulator can be shown failing.
//
// It is deliberately NOT a container and NOT an emulator. It is the whole
// routed path collapsed into one function: the sidecar's live credential
// tripwire, then a dispatch between the covered and the uncovered probe, then
// an in memory store that a covered write adds to. That is exactly the surface
// the suite asserts on and nothing more, because a fake with more surface than
// the suite reads would be a second implementation to keep correct.
//
// The tripwire calls the REAL livekey.ScanHeaders rather than comparing
// strings against the declared vector. A fake that refused by string equality
// would pass LiveCredential_IsRefused for a vector the real detector cannot
// see, and the suite would then certify a tripwire that never fires.

// The shapes. Two of them, for the reason the datastore suite has two: a skip
// is a legitimate answer for one emulator and it is not an answer for every
// emulator, and a behaviour skipped by every shape has never run at all.
const (
	// emuShapeObject is an object store: the covered probe creates a bucket,
	// there is an uncovered operation, and there is a live credential vector.
	// It runs all nine behaviours.
	emuShapeObject = "object"
	// emuShapeQueue is a read only covered probe with no uncovered operation
	// and no credential vector. It exists to prove that Creates false is a
	// legitimate answer rather than a defect: a read only operation is a
	// perfectly good thing for an emulator to be covered by.
	emuShapeQueue = "queue"
)

// The flaws. One per assertion in the suite, which is what makes each of them
// a control for that assertion rather than for its behaviour in general.
const (
	emuFlawNone                    = "none"
	emuFlawNoName                  = "no-name"
	emuFlawNoHosts                 = "no-hosts"
	emuFlawHostIsAURL              = "host-is-a-url"
	emuFlawTransportError          = "transport-error"
	emuFlawRefusesEverything       = "refuses-everything"
	emuFlawCoveredCarriesErrorCode = "covered-carries-error-code"
	emuFlawAnswersUncovered        = "answers-uncovered"
	emuFlawWrongErrorContentType   = "wrong-error-content-type"
	emuFlawUncoveredWithoutCode    = "uncovered-without-code"
	emuFlawEmptyState              = "empty-state"
	emuFlawNamelessStateItem       = "nameless-state-item"
	emuFlawResetKeepsState         = "reset-keeps-state"
	emuFlawLiveCredentialAccepted  = "live-credential-accepted"
	emuFlawSilentRefusal           = "silent-refusal"
	emuFlawHasARouteOut            = "has-a-route-out"
)

// awsExampleKey is a key shaped like a live one and belonging to nobody.
//
// Assembled at run time rather than written down, which is the convention
// engine/pkg/livekey's own tests follow and it is not decoration: a literal
// that looks like a credential is a literal the repository scanner refuses,
// and a vector carved out with an exception is a vector the real detector is
// no longer being asked about. livekey recognises AKIA followed by at least
// sixteen characters, so this is detected by the shipping detector.
var awsExampleKey = "AKIA" + strings.Repeat("A", 16)

var awsExampleAuthorization = "AWS4-HMAC-SHA256 Credential=" + awsExampleKey +
	"/20260907/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-date, Signature=" +
	strings.Repeat("0", 64)

// fakeEmulator is one emulator handle. The store is shared across the handles
// one run creates, because RunEmulator builds a fresh handle per behaviour and
// the state has to survive between the probe and the listing inside one.
type fakeEmulator struct {
	shape string
	flaw  string
	store *fakeEmulatorStore
}

type fakeEmulatorStore struct {
	mu    sync.Mutex
	items []provider.EmulatorStateItem
	// n numbers the buckets, so that two covered probes in one behaviour
	// create two items rather than one.
	n int
}

func newFakeEmulatorStore() *fakeEmulatorStore { return &fakeEmulatorStore{} }

func newFakeEmulator(store *fakeEmulatorStore, shape, flaw string) *fakeEmulator {
	return &fakeEmulator{shape: shape, flaw: flaw, store: store}
}

func (f *fakeEmulator) Name() string {
	if f.flaw == emuFlawNoName {
		return ""
	}
	return "fake-" + f.shape
}

func (f *fakeEmulator) Hosts() []string {
	switch f.flaw {
	case emuFlawNoHosts:
		return nil
	case emuFlawHostIsAURL:
		return []string{"https://s3.us-east-1.amazonaws.com/"}
	}
	return []string{"s3.us-east-1.amazonaws.com", "af-conformance.s3.us-east-1.amazonaws.com"}
}

func (f *fakeEmulator) Capabilities() provider.EmulatorCaps {
	if f.shape == emuShapeQueue {
		return provider.EmulatorCaps{
			// A listing. It creates nothing, which is why this shape skips the
			// two state behaviours instead of failing them.
			Covered: provider.EmulatorProbe{Method: http.MethodGet, Path: "/", Creates: false},
		}
	}
	return provider.EmulatorCaps{
		Covered: provider.EmulatorProbe{
			Method: http.MethodPut, Path: "/af-conformance", Creates: true,
		},
		Uncovered: provider.EmulatorProbe{
			Method: http.MethodPost, Path: "/af-conformance", Query: "select&select-type=2",
		},
		ErrorContentType: "text/xml",
		ErrorCode:        "NotImplemented",
		LiveCredential:   awsExampleAuthorization,
	}
}

func (f *fakeEmulator) RoundTrip(_ context.Context, req *http.Request) (*http.Response, error) {
	if f.flaw == emuFlawTransportError {
		return nil, errors.New("dial tcp: no route to the emulator")
	}

	// The tripwire, which is what the sidecar runs on all three of its paths
	// before the mode is acted on.
	if f.flaw != emuFlawLiveCredentialAccepted {
		if found := livekey.ScanHeaders(req.Header); len(found) > 0 {
			body := "Antifailure refused this request because it carries a live credential.\n\n" +
				"  Found: " + livekey.Describe(found) + "\n"
			if f.flaw == emuFlawSilentRefusal {
				// Refused, and the reader is not told what about. The status is
				// right and the sentence is missing, which is the second
				// assertion in that behaviour and nothing else.
				body = "Forbidden\n"
			}
			return fakeEmulatorReply(http.StatusForbidden, "text/plain; charset=utf-8", body), nil
		}
	}

	if f.flaw == emuFlawRefusesEverything {
		return fakeEmulatorReply(http.StatusForbidden, "text/xml",
			"<Error><Code>AccessDenied</Code></Error>"), nil
	}

	caps := f.Capabilities()
	if caps.Uncovered.Query != "" && req.URL.RawQuery == caps.Uncovered.Query {
		return f.uncovered(), nil
	}
	return f.covered(req), nil
}

// covered answers the operation this emulator claims to implement.
func (f *fakeEmulator) covered(req *http.Request) *http.Response {
	if f.flaw == emuFlawCoveredCarriesErrorCode {
		// 200 with an error document, which is a real AWS shape and is the
		// reason Covered_IsNotRefused reads the body as well as the status.
		return fakeEmulatorReply(http.StatusOK, "text/xml",
			"<Error><Code>NotImplemented</Code></Error>")
	}
	if f.shape == emuShapeQueue {
		return fakeEmulatorReply(http.StatusOK, "text/xml", "<ListAllMyBucketsResult/>")
	}
	if f.flaw != emuFlawEmptyState {
		f.store.mu.Lock()
		f.store.n++
		// Numbered, so that two covered probes in one behaviour add two items
		// rather than one. The suite compares state items by value, and an
		// unnumbered name would make the second write look like nothing
		// happened, which is a different behaviour's failure entirely.
		item := provider.EmulatorStateItem{
			Kind: "bucket",
			Name: fmt.Sprintf("%s-%d", strings.TrimPrefix(req.URL.Path, "/"), f.store.n),
		}
		if f.flaw == emuFlawNamelessStateItem {
			// The KIND is dropped rather than the name, and for a reason that
			// is about the control rather than about emulators: an item whose
			// name is empty is identical to the last one, the suite sees
			// nothing added, and this flaw would red State_IsEnumerable on the
			// assertion the empty-state flaw already controls. Dropping the
			// kind leaves the item unique, so the only assertion it can reach
			// is the one that reads the fields.
			item.Kind = ""
		}
		f.store.items = append(f.store.items, item)
		f.store.mu.Unlock()
	}
	return fakeEmulatorReply(http.StatusOK, "text/xml", "")
}

// uncovered answers an operation this emulator has not implemented.
func (f *fakeEmulator) uncovered() *http.Response {
	switch f.flaw {
	case emuFlawAnswersUncovered:
		// The shape and the code are right and the STATUS says it worked, so
		// this reds the status assertion and nothing else.
		return fakeEmulatorReply(http.StatusOK, "text/xml",
			"<Error><Code>NotImplemented</Code></Error>")
	case emuFlawWrongErrorContentType:
		return fakeEmulatorReply(http.StatusNotImplemented, "application/json",
			`{"code":"NotImplemented"}`)
	case emuFlawUncoveredWithoutCode:
		return fakeEmulatorReply(http.StatusNotImplemented, "text/xml",
			"<Error><Code>InternalError</Code></Error>")
	}
	return fakeEmulatorReply(http.StatusNotImplemented, "text/xml",
		"<Error><Code>NotImplemented</Code>"+
			"<Message>The API operation is not implemented by this emulator.</Message></Error>")
}

func (f *fakeEmulator) State(context.Context) ([]provider.EmulatorStateItem, error) {
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	out := append([]provider.EmulatorStateItem(nil), f.store.items...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *fakeEmulator) Reset(context.Context) error {
	if f.flaw == emuFlawResetKeepsState {
		return nil
	}
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	f.store.items = nil
	return nil
}

func (f *fakeEmulator) Reach(_ context.Context, address string) error {
	if f.flaw == emuFlawHasARouteOut {
		return nil
	}
	return fmt.Errorf("dial tcp %s: network is unreachable", address)
}

func (f *fakeEmulator) Close() error { return nil }

// fakeEmulatorReply builds a response the way a server would.
func fakeEmulatorReply(status int, contentType, body string) *http.Response {
	h := http.Header{}
	h.Set("Content-Type", contentType)
	return &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        h,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

// The interface is satisfied, checked at compile time rather than by the
// suite, so that a change to provider.Emulator fails here first.
var _ provider.Emulator = (*fakeEmulator)(nil)

// TestFakeEmulator_TripwireVectorIsDetectedByTheRealDetector guards the one
// assumption the fake makes that the suite cannot see.
//
// LiveCredential_IsRefused is only a real control if the vector it sends is
// one livekey actually recognises. A vector the detector cannot see would make
// the fake's refusal unreachable, the behaviour would red for the wrong
// reason, and the flaw that switches the tripwire off would look like it
// worked.
func TestFakeEmulator_TripwireVectorIsDetectedByTheRealDetector(t *testing.T) {
	t.Parallel()
	h := http.Header{}
	h.Set("Authorization", awsExampleAuthorization)
	found := livekey.ScanHeaders(h)
	if len(found) == 0 {
		t.Fatalf("livekey does not recognise the conformance suite's own live credential "+
			"vector %q, so LiveCredential_IsRefused would pass against an emulator with no "+
			"tripwire at all", awsExampleKey)
	}
}
