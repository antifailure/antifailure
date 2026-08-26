package webhook_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/webhook"
)

var now = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

const secret = "whsec_test_not_a_real_secret_value"

func TestBuild_StripeSignatureVerifiesTheWayStripesLibraryDoes(t *testing.T) {
	t.Parallel()
	// Reimplemented here rather than calling our own signer, because a test
	// that calls the code under test to check the code under test proves
	// nothing. An application verifying signatures, which is every
	// application that should, has to accept this.
	e, err := webhook.Build("stripe", "checkout.session.completed", secret, nil, now)
	require.NoError(t, err)

	sig := e.Headers["Stripe-Signature"]
	require.NotEmpty(t, sig)

	var ts, v1 string
	for _, part := range strings.Split(sig, ",") {
		k, v, _ := strings.Cut(part, "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			v1 = v
		}
	}
	require.Equal(t, strconv.FormatInt(now.Unix(), 10), ts)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(e.Body)
	require.Equal(t, hex.EncodeToString(mac.Sum(nil)), v1)
}

func TestBuild_StripeSignsTheTimestampWithTheBody(t *testing.T) {
	t.Parallel()
	// Signing the body alone would verify today and let somebody replay the
	// captured request forever. The timestamp is what makes a captured
	// signature useless a few minutes later.
	early, err := webhook.Build("stripe", "invoice.paid", secret, nil, now)
	require.NoError(t, err)
	later, err := webhook.Build("stripe", "invoice.paid", secret, nil, now.Add(time.Minute))
	require.NoError(t, err)
	require.NotEqual(t, early.Headers["Stripe-Signature"], later.Headers["Stripe-Signature"])
}

func TestBuild_StripeEnvelopeIsTheShapeAnApplicationParses(t *testing.T) {
	t.Parallel()
	// An application parsing the wrong envelope fails on a field that is not
	// there, which looks like a bug in the application rather than in this.
	e, err := webhook.Build("stripe", "customer.subscription.deleted", secret, nil, now)
	require.NoError(t, err)

	var env struct {
		ID       string `json:"id"`
		Object   string `json:"object"`
		Type     string `json:"type"`
		Livemode bool   `json:"livemode"`
		Created  int64  `json:"created"`
		Data     struct {
			Object map[string]any `json:"object"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(e.Body, &env))
	require.Equal(t, "event", env.Object)
	require.Equal(t, "customer.subscription.deleted", env.Type)
	require.False(t, env.Livemode, "a simulated event is never live mode")
	require.Equal(t, now.Unix(), env.Created)
	require.Equal(t, "canceled", env.Data.Object["status"])
	require.True(t, strings.HasPrefix(env.ID, "evt_"))
}

func TestBuild_OverridesGoIntoTheDataObject(t *testing.T) {
	t.Parallel()
	// So a caller can say which subscription the event is about without
	// writing the whole envelope by hand.
	e, err := webhook.Build("stripe", "customer.subscription.updated", secret,
		map[string]any{"id": "sub_specific", "cancel_at_period_end": true}, now)
	require.NoError(t, err)

	var env struct {
		Data struct {
			Object map[string]any `json:"object"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(e.Body, &env))
	require.Equal(t, "sub_specific", env.Data.Object["id"])
	require.Equal(t, true, env.Data.Object["cancel_at_period_end"])
	require.Equal(t, "subscription", env.Data.Object["object"], "the sample's own fields survive")
}

func TestBuild_GitHubPutsThePayloadAtTheTopLevel(t *testing.T) {
	t.Parallel()
	e, err := webhook.Build("github", "push", secret, nil, now)
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, json.Unmarshal(e.Body, &body))
	require.Equal(t, "refs/heads/main", body["ref"], "GitHub does not wrap its payload")
	require.Contains(t, e.Headers["X-Hub-Signature-256"], "sha256=")
	require.NotEmpty(t, e.Headers["X-GitHub-Delivery"])
}

func TestBuild_ResendUsesSvixHeaders(t *testing.T) {
	t.Parallel()
	e, err := webhook.Build("resend", "email.delivered", secret, nil, now)
	require.NoError(t, err)
	require.NotEmpty(t, e.Headers["svix-id"])
	require.NotEmpty(t, e.Headers["svix-timestamp"])
	require.Contains(t, e.Headers["svix-signature"], "v1,")
}

func TestVerify_AcceptsWhatBuildProducedAndRejectsATamperedBody(t *testing.T) {
	t.Parallel()
	for _, provider := range webhook.Names() {
		t.Run(provider, func(t *testing.T) {
			events := webhook.EventNames(provider)
			require.NotEmpty(t, events)

			e, err := webhook.Build(provider, events[0], secret, nil, now)
			require.NoError(t, err)
			require.True(t, webhook.Verify(provider, e.Body, e.Headers, secret, now))

			tampered := append([]byte(nil), e.Body...)
			tampered[len(tampered)-2] = 'X'
			require.False(t, webhook.Verify(provider, tampered, e.Headers, secret, now),
				"a changed body must not verify")

			require.False(t, webhook.Verify(provider, e.Body, e.Headers, "a-different-secret", now),
				"the wrong secret must not verify")
		})
	}
}

func TestBuild_RefusesAnUnknownProviderOrEventAndSaysWhatThereIs(t *testing.T) {
	t.Parallel()
	// A list of what exists turns a dead end into the next thing to type.
	_, err := webhook.Build("nonesuch", "anything", secret, nil, now)
	require.Error(t, err)
	require.Contains(t, err.Error(), "stripe")

	_, err = webhook.Build("stripe", "not.an.event", secret, nil, now)
	require.Error(t, err)
	require.Contains(t, err.Error(), "checkout.session.completed")
}

func TestNames_AreStableAndCoverTheFlowsThatNeedThem(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{"github", "resend", "stripe"}, webhook.Names())
	// The events a subscription flow actually waits on.
	stripe := webhook.EventNames("stripe")
	for _, want := range []string{
		"checkout.session.completed", "customer.subscription.created",
		"customer.subscription.deleted", "invoice.paid", "invoice.payment_failed",
	} {
		require.Contains(t, stripe, want)
	}
	require.Nil(t, webhook.EventNames("nonesuch"))
}
