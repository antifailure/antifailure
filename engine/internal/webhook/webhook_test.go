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
	"github.com/antifailure/antifailure/engine/pkg/schema"
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

func TestBuild_TwoEventsInOneSecondHaveDifferentIdentifiers(t *testing.T) {
	t.Parallel()
	// A Stripe webhook handler must be idempotent on the event id, because
	// Stripe retries. The id used to be "evt_afmock" plus the unix second, so a
	// subscription created and an invoice paid in the same second carried the
	// SAME id, and a correct handler dropped the second as a repeat of the
	// first. That is invisible until somebody builds the integration this
	// simulator exists for, which is exactly what it exists for.
	at := time.Unix(1767225600, 0).UTC()
	signing := webhook.SecretFor("env-1", "stripe")

	seen := map[string]string{}
	for _, eventType := range webhook.EventNames("stripe") {
		event, err := webhook.Build("stripe", eventType, signing, nil, at)
		require.NoError(t, err)
		var envelope struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(event.Body, &envelope))
		require.NotContains(t, seen, envelope.ID,
			"%s and %s were signed in the same second and share the id %s",
			eventType, seen[envelope.ID], envelope.ID)
		seen[envelope.ID] = eventType
	}
	require.Len(t, seen, len(webhook.EventNames("stripe")))
}

func TestBuild_TheSameEventTwiceKeepsItsIdentifier(t *testing.T) {
	t.Parallel()
	// The fixed clock exists so two runs of one workflow produce the same
	// output and can be compared. Making the identifier unique must not cost
	// that, so it is a digest of the event rather than a counter: the same
	// event with the same payload at the same instant is the same event, and an
	// application dropping the repeat is behaving correctly.
	at := time.Unix(1767225600, 0).UTC()
	signing := webhook.SecretFor("env-1", "stripe")

	first, err := webhook.Build("stripe", "invoice.paid", signing, map[string]any{"id": "in_1"}, at)
	require.NoError(t, err)
	second, err := webhook.Build("stripe", "invoice.paid", signing, map[string]any{"id": "in_1"}, at)
	require.NoError(t, err)
	require.Equal(t, string(first.Body), string(second.Body))

	// A different payload is a different event.
	other, err := webhook.Build("stripe", "invoice.paid", signing, map[string]any{"id": "in_2"}, at)
	require.NoError(t, err)
	require.NotEqual(t, string(first.Body), string(other.Body))
}

func TestSecrets_OneDerivationForEveryCaller(t *testing.T) {
	t.Parallel()
	rules := []schema.EgressRule{
		{Host: "api.resend.com", Mode: schema.ModeCapture},
		{Host: "api.stripe.com", Mode: schema.ModeMock, WebhookPath: "/webhooks/stripe"},
		{Host: "api.github.com", Mode: schema.ModeBlock, WebhookPath: "/webhooks/github"},
	}
	getenv := func(name string) string {
		if name == "GITHUB_WEBHOOK_SECRET" {
			return "from-the-shell"
		}
		return ""
	}

	got := webhook.Secrets(rules, "env-1", getenv)

	// A rule with no webhook path contributes nothing, so resend is absent.
	require.Len(t, got, 2)
	// Derived from the environment identifier, the same way the sender
	// derives it, so the two agree without sharing state.
	require.Equal(t, webhook.SecretFor("env-1", "stripe"), got["STRIPE_WEBHOOK_SECRET"])
	// A value somebody set in the shell wins, because they are matching a
	// fixture recorded elsewhere.
	require.Equal(t, "from-the-shell", got["GITHUB_WEBHOOK_SECRET"])
	// Nil getenv is a caller with no shell to consult, not a crash.
	require.Equal(t, webhook.SecretFor("env-1", "github"),
		webhook.Secrets(rules, "env-1", nil)["GITHUB_WEBHOOK_SECRET"])
}

func TestBuild_EveryGitHubSampleNamesTheAccountItIsAbout(t *testing.T) {
	t.Parallel()
	// The application scopes every statement to the account a delivery names,
	// through installation.account.login or repository.owner.login, and a
	// payload with neither is acknowledged as "the payload names no account"
	// and handled nowhere. The samples used to be exactly that, so the events
	// the simulator existed to rehearse were the ones it could not reach.
	names := webhook.EventNames("github")
	require.Contains(t, names, "installation",
		"the first event a new customer's control plane receives is the installation itself")
	for _, name := range names {
		event, err := webhook.Build("github", name, "s", nil, time.Unix(1767225600, 0))
		require.NoError(t, err)
		var payload struct {
			Installation *struct {
				ID      int `json:"id"`
				Account *struct {
					Login string `json:"login"`
				} `json:"account"`
			} `json:"installation"`
			Repository *struct {
				Owner struct {
					Login string `json:"login"`
				} `json:"owner"`
			} `json:"repository"`
		}
		require.NoError(t, json.Unmarshal(event.Body, &payload))
		named := (payload.Installation != nil && payload.Installation.Account != nil &&
			payload.Installation.Account.Login != "") ||
			(payload.Repository != nil && payload.Repository.Owner.Login != "")
		require.True(t, named, "%s names no account", name)
	}
}

func TestBuild_GitHubNamesTheEventAndGivesEachDeliveryItsOwnIdentifier(t *testing.T) {
	t.Parallel()
	// GitHub puts the event name in a header, and the sender did not set it,
	// so every delivery reached the application as "unknown" and was
	// acknowledged and acted on nowhere. The delivery identifier was a
	// constant, so the second delivery of anything was fenced as a replay of
	// the first by an application that keys its ledger on it, which is what a
	// correct application does.
	at := time.Unix(1767225600, 0).UTC()
	installation, err := webhook.Build("github", "installation", "s", nil, at)
	require.NoError(t, err)
	pullRequest, err := webhook.Build("github", "pull_request", "s", nil, at)
	require.NoError(t, err)

	require.Equal(t, "installation", installation.Headers["X-GitHub-Event"])
	require.Equal(t, "pull_request", pullRequest.Headers["X-GitHub-Event"])
	require.NotEqual(t, installation.Headers["X-GitHub-Delivery"], pullRequest.Headers["X-GitHub-Delivery"],
		"two deliveries in one second must not share an identifier")
	require.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-8[0-9a-f]{3}-[0-9a-f]{12}$`,
		installation.Headers["X-GitHub-Delivery"], "GitHub's identifier is a UUID and an application may check the shape")

	// The same event with the same payload in the same second is the same
	// delivery, so two runs of one workflow can be compared.
	again, err := webhook.Build("github", "installation", "s", nil, at)
	require.NoError(t, err)
	require.Equal(t, installation.Headers["X-GitHub-Delivery"], again.Headers["X-GitHub-Delivery"])
	// And the signature still verifies with the extra headers present.
	require.True(t, webhook.Verify("github", installation.Body, installation.Headers, "s", at))
}

func TestBuild_APinnedEventIdentifierMakesARetry(t *testing.T) {
	t.Parallel()
	// Two triggers a second apart are two different events, which is right,
	// and it means a retry, the same event with the same id, could not be
	// rehearsed except by racing the clock. The override pins the id and
	// touches nothing else in the payload.
	first, err := webhook.Build("stripe", "invoice.paid", "s",
		map[string]any{"event_id": "evt_retry_1", "amount_paid": 4900}, time.Unix(1767225600, 0))
	require.NoError(t, err)
	second, err := webhook.Build("stripe", "invoice.paid", "s",
		map[string]any{"event_id": "evt_retry_1", "amount_paid": 4900}, time.Unix(1767225601, 0))
	require.NoError(t, err)
	var a, b struct {
		ID   string `json:"id"`
		Data struct {
			Object map[string]any `json:"object"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body, &a))
	require.NoError(t, json.Unmarshal(second.Body, &b))
	require.Equal(t, "evt_retry_1", a.ID)
	require.Equal(t, a.ID, b.ID, "the same event a second later is the same event")
	require.NotContains(t, a.Data.Object, "event_id", "the override is not a payload field")
	require.Equal(t, float64(4900), a.Data.Object["amount_paid"])

	gh, err := webhook.Build("github", "installation", "s",
		map[string]any{"event_id": "11111111-2222-4333-8444-555555555555"}, time.Unix(1767225600, 0))
	require.NoError(t, err)
	require.Equal(t, "11111111-2222-4333-8444-555555555555", gh.Headers["X-GitHub-Delivery"])
}
