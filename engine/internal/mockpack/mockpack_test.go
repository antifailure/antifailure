package mockpack_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/mockpack"
)

func stripe(t *testing.T) *mockpack.Engine {
	t.Helper()
	packs, err := mockpack.Builtin()
	require.NoError(t, err)
	return mockpack.New(packs)
}

func decode(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(body, &out), "response is not JSON: %s", body)
	return out
}

func TestBuiltin_LoadsAndValidates(t *testing.T) {
	t.Parallel()
	packs, err := mockpack.Builtin()
	require.NoError(t, err)
	require.NotEmpty(t, packs)

	names := mockpack.New(packs).Names()
	require.Contains(t, names, "stripe")
}

func TestStripe_CreateThenReadReturnsTheSameObject(t *testing.T) {
	t.Parallel()
	// A create followed by a read that returns nothing is not a mock of
	// anything. This is the property that separates a stateful pack from a
	// list of canned answers.
	e := stripe(t)

	created, ok := e.Answer("api.stripe.com", "POST", "/v1/customers",
		[]byte(`{"email":"buyer@example.test","name":"A Buyer"}`))
	require.True(t, ok)
	require.Equal(t, 200, created.Status)

	obj := decode(t, created.Body)
	id, _ := obj["id"].(string)
	require.True(t, strings.HasPrefix(id, "cus_"), "id was %q", id)
	require.Equal(t, "buyer@example.test", obj["email"], "the request's own values come back")
	require.Equal(t, "customer", obj["object"])
	require.Equal(t, false, obj["livemode"], "a mock is never live mode")

	read, ok := e.Answer("api.stripe.com", "GET", "/v1/customers/"+id, nil)
	require.True(t, ok)
	require.Equal(t, 200, read.Status)
	require.Equal(t, id, decode(t, read.Body)["id"])
}

func TestStripe_ReadingSomethingThatWasNeverCreatedUsesStripesErrorShape(t *testing.T) {
	t.Parallel()
	// An application handling a missing object expects the provider's error
	// shape, not a bare 404, and the message says why it is missing rather
	// than leaving somebody to wonder.
	e := stripe(t)
	resp, ok := e.Answer("api.stripe.com", "GET", "/v1/customers/cus_neverexisted", nil)
	require.True(t, ok)
	require.Equal(t, 404, resp.Status)

	body := decode(t, resp.Body)
	errObj, isMap := body["error"].(map[string]any)
	require.True(t, isMap, "Stripe wraps errors in an error object")
	require.Equal(t, "invalid_request_error", errObj["type"])
	require.Equal(t, "resource_missing", errObj["code"])
	require.Contains(t, errObj["message"], "mock pack",
		"the message says why it is missing rather than leaving somebody to wonder")
}

func TestStripe_ListReturnsWhatWasCreatedInOrder(t *testing.T) {
	t.Parallel()
	e := stripe(t)
	for _, email := range []string{"a@example.test", "b@example.test", "c@example.test"} {
		_, ok := e.Answer("api.stripe.com", "POST", "/v1/customers",
			[]byte(`{"email":"`+email+`"}`))
		require.True(t, ok)
	}
	resp, ok := e.Answer("api.stripe.com", "GET", "/v1/customers", nil)
	require.True(t, ok)

	body := decode(t, resp.Body)
	require.Equal(t, "list", body["object"])
	items, isList := body["data"].([]any)
	require.True(t, isList, "the items go in the list rather than replacing it as a string")
	require.Len(t, items, 3)
	require.Equal(t, "a@example.test", items[0].(map[string]any)["email"],
		"insertion order, not map order, so two runs agree")
}

func TestStripe_RunsAWholeBillingFlow(t *testing.T) {
	t.Parallel()
	// The bar the pack has to clear: checkout, subscribe, read back, cancel.
	// An application talking to a mock that answers plausibly but wrongly does
	// not fail at the mock, it fails three steps later somewhere that looks
	// like its own bug.
	e := stripe(t)

	customer := decode(t, mustAnswer(t, e, "POST", "/v1/customers", `{"email":"buyer@example.test"}`))
	cid := customer["id"].(string)

	session := decode(t, mustAnswer(t, e, "POST", "/v1/checkout/sessions",
		`{"mode":"subscription","customer":"`+cid+`","success_url":"https://shop.test/ok"}`))
	require.Equal(t, "checkout.session", session["object"])
	require.Equal(t, cid, session["customer"])
	require.Contains(t, session["url"], "checkout.stripe.com",
		"the application redirects to this, so it has to look like a URL")

	sub := decode(t, mustAnswer(t, e, "POST", "/v1/subscriptions",
		`{"customer":"`+cid+`"}`))
	sid := sub["id"].(string)
	require.Equal(t, "active", sub["status"])

	read := decode(t, mustAnswer(t, e, "GET", "/v1/subscriptions/"+sid, ""))
	require.Equal(t, "active", read["status"], "the subscription that was created is the one that is read")

	cancelled := decode(t, mustAnswer(t, e, "DELETE", "/v1/subscriptions/"+sid, ""))
	require.Equal(t, "canceled", cancelled["status"])

	after := decode(t, mustAnswer(t, e, "GET", "/v1/subscriptions/"+sid, ""))
	require.Equal(t, "canceled", after["status"],
		"cancelling has to change what a later read returns, or the flow cannot be asserted on")
}

func mustAnswer(t *testing.T, e *mockpack.Engine, method, path, body string) []byte {
	t.Helper()
	resp, ok := e.Answer("api.stripe.com", method, path, []byte(body))
	require.True(t, ok, "no route matched %s %s", method, path)
	require.Less(t, resp.Status, 400, "%s %s answered %d: %s", method, path, resp.Status, resp.Body)
	return resp.Body
}

func TestAnswer_ReportsAMissRatherThanGuessing(t *testing.T) {
	t.Parallel()
	// Returning an empty 200 for an unmatched request is how an application
	// carries on with nothing and fails somewhere unrelated.
	e := stripe(t)
	_, ok := e.Answer("api.stripe.com", "POST", "/v1/nothing_like_this", nil)
	require.False(t, ok)

	_, ok = e.Answer("api.somewhere-else.test", "GET", "/v1/customers", nil)
	require.False(t, ok, "a pack answers only for the hosts it names")
}

func TestHandles_MatchesTheHostsThePackNames(t *testing.T) {
	t.Parallel()
	e := stripe(t)
	require.True(t, e.Handles("api.stripe.com"))
	require.True(t, e.Handles("API.Stripe.com."))
	require.True(t, e.Handles("checkout.stripe.com"))
	require.False(t, e.Handles("api.notstripe.com"))
}

func TestMatch_ALiteralBeatsAPlaceholder(t *testing.T) {
	t.Parallel()
	// So a pack author does not have to think about the order they wrote the
	// routes in.
	pack, err := mockpack.Parse([]byte(`{
		"name":"t","hosts":["t.test"],
		"routes":[
			{"method":"GET","path":"/v1/things/{id}","body":{"which":"placeholder"}},
			{"method":"GET","path":"/v1/things/special","body":{"which":"literal"}}
		]}`))
	require.NoError(t, err)
	e := mockpack.New([]mockpack.Pack{pack})

	resp, ok := e.Answer("t.test", "GET", "/v1/things/special", nil)
	require.True(t, ok)
	require.Equal(t, "literal", decodeField(t, resp.Body, "which"))

	resp, ok = e.Answer("t.test", "GET", "/v1/things/anything", nil)
	require.True(t, ok)
	require.Equal(t, "placeholder", decodeField(t, resp.Body, "which"))
}

func TestMatch_ACatchAllNeverBeatsASpecificRoute(t *testing.T) {
	t.Parallel()
	pack, err := mockpack.Parse([]byte(`{
		"name":"t","hosts":["t.test"],
		"routes":[
			{"path":"/**","body":{"which":"catchall"}},
			{"method":"GET","path":"/v1/thing","body":{"which":"specific"}}
		]}`))
	require.NoError(t, err)
	e := mockpack.New([]mockpack.Pack{pack})

	resp, _ := e.Answer("t.test", "GET", "/v1/thing", nil)
	require.Equal(t, "specific", decodeField(t, resp.Body, "which"))

	resp, ok := e.Answer("t.test", "GET", "/anything/else", nil)
	require.True(t, ok)
	require.Equal(t, "catchall", decodeField(t, resp.Body, "which"))
}

func TestFill_LeavesNoPlaceholderBehind(t *testing.T) {
	t.Parallel()
	// A literal {request.email} in a response is the placeholder leakage that
	// proves nobody looked at the output.
	e := stripe(t)
	resp, ok := e.Answer("api.stripe.com", "POST", "/v1/customers", []byte(`{}`))
	require.True(t, ok)
	require.NotContains(t, string(resp.Body), "{request.")
	require.NotContains(t, string(resp.Body), "{id:")
	require.NotContains(t, string(resp.Body), "{now}")
}

func TestFill_ReadsAFormBodyAsWellAsJSON(t *testing.T) {
	t.Parallel()
	// Stripe's own libraries send forms, so a pack that only understood JSON
	// would return empty fields for every real Stripe client.
	e := stripe(t)
	resp, ok := e.Answer("api.stripe.com", "POST", "/v1/customers",
		[]byte("email=buyer%40example.test&name=A+Buyer"))
	require.True(t, ok)
	obj := decode(t, resp.Body)
	require.Equal(t, "buyer@example.test", obj["email"])
	require.Equal(t, "A Buyer", obj["name"])
}

func TestGenerate_ProducesADifferentIdentifierEachTime(t *testing.T) {
	t.Parallel()
	e := stripe(t)
	first := decode(t, mustAnswer(t, e, "POST", "/v1/customers", `{}`))["id"]
	second := decode(t, mustAnswer(t, e, "POST", "/v1/customers", `{}`))["id"]
	require.NotEqual(t, first, second, "two customers with one id is not a mock of Stripe")
}

func TestParse_RefusesAPackThatWouldAnswerNothing(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"no name":   `{"hosts":["t.test"],"routes":[{"path":"/"}]}`,
		"no routes": `{"name":"t","hosts":["t.test"]}`,
		"no path":   `{"name":"t","hosts":["t.test"],"routes":[{"method":"GET"}]}`,
		"not json":  `{`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := mockpack.Parse([]byte(body))
			require.Error(t, err)
		})
	}
}

func TestSkeleton_IsAPackSomebodyCanPaste(t *testing.T) {
	t.Parallel()
	// Handing back the shape to fill in is the difference between a dead end
	// and a two minute fix.
	s := mockpack.Skeleton("api.example.com", "post", "/v1/things")
	pack, err := mockpack.Parse([]byte(s))
	require.NoError(t, err, "the skeleton has to be a valid pack: %s", s)
	require.Equal(t, []string{"api.example.com"}, pack.Hosts)
	require.Equal(t, "POST", pack.Routes[0].Method)
}

func decodeField(t *testing.T, body []byte, field string) string {
	t.Helper()
	var obj map[string]any
	require.NoError(t, json.Unmarshal(body, &obj))
	s, _ := obj[field].(string)
	return s
}

// A pack that stores what was created and returns it on the next read is a
// mock of the provider; one that does not is a list of canned answers. The
// fidelity inventory reports the two differently, so the difference has to be
// something the package answers rather than something a caller judges.
func TestStateful_SeparatesAMockFromCannedAnswers(t *testing.T) {
	t.Parallel()
	packs, err := mockpack.Builtin()
	require.NoError(t, err)
	require.NotEmpty(t, packs)
	for _, p := range packs {
		require.True(t, p.Stateful(),
			"the built in %s pack no longer keeps state, which the inventory reports on", p.Name)
	}

	canned, err := mockpack.Parse([]byte(
		`{"name":"canned","hosts":["t.test"],"routes":[{"path":"/v1/things","body":{"ok":true}}]}`))
	require.NoError(t, err)
	require.False(t, canned.Stateful())
}

// PackFor has to name the pack Answer would consult, or the inventory reports
// cover a request would not get.
func TestPackFor_NamesThePackThatWouldAnswer(t *testing.T) {
	t.Parallel()
	packs, err := mockpack.Builtin()
	require.NoError(t, err)
	engine := mockpack.New(packs)

	pack, ok := engine.PackFor("api.stripe.com")
	require.True(t, ok)
	require.Equal(t, "stripe", pack.Name)
	require.True(t, engine.Handles("api.stripe.com"))

	resp, answered := engine.Answer("api.stripe.com", "POST", "/v1/customers", []byte(`{}`))
	require.True(t, answered, "PackFor named a pack that does not answer")
	require.Equal(t, pack.Name, resp.Pack)

	_, ok = engine.PackFor("api.example.invalid")
	require.False(t, ok)
	require.False(t, engine.Handles("api.example.invalid"))
}
