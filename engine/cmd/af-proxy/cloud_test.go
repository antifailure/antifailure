package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/detect"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// What the sidecar does with the cloud, as opposed to what the catalog says
// about it.
//
// The catalog registered *.amazonaws.com under "Amazon SES" in capture mode,
// so an S3 PUT matched a mail rule, fell through to the generic capture
// handler and came back 200 with an empty body. The application had every
// reason to believe the object was stored. It never left the environment and
// nothing said so.
//
// These tests drive the real sidecar with the egress section af init actually
// writes, rather than with rules typed out here, because the defect was in the
// catalog and a hand written policy would have been correct by construction.

// cloudEgress is the egress section af init writes for a repository that uses
// the AWS, Google and Azure SDKs.
func cloudEgress(t *testing.T) *schema.Egress {
	t.Helper()
	files := fstest.MapFS{"package.json": &fstest.MapFile{Mode: 0o644, Data: []byte(
		`{"name":"shopfront","dependencies":{
			"@aws-sdk/client-s3":"3.0.0","@aws-sdk/client-sqs":"3.0.0",
			"@aws-sdk/client-secrets-manager":"3.0.0","@aws-sdk/client-sesv2":"3.0.0",
			"@google-cloud/storage":"7.0.0","@azure/keyvault-secrets":"4.0.0"}}`)}}

	res, err := detect.Run(context.Background(), files, "shopfront", detect.Options{
		Clock: clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
	})
	require.NoError(t, err)
	require.NotNil(t, res.Draft.Egress, "af init wrote no egress section at all")
	return res.Draft.Egress
}

func TestCloud_AnS3PutIsRefusedByARuleThatNamesS3(t *testing.T) {
	s := newSidecar(t, cloudEgress(t))

	put, err := http.NewRequest(http.MethodPut,
		"http://s3.amazonaws.com/shopfront-uploads/invoice.pdf", strings.NewReader("%PDF-1.7"))
	require.NoError(t, err)
	resp := through(t, s, put)

	require.Equal(t, http.StatusForbidden, resp.StatusCode,
		"an object write from a preview environment lands in the real bucket")
	text := body(t, resp)
	require.Contains(t, text, "s3.amazonaws.com",
		"the refusal has to name the rule, or a developer cannot find it")
	require.Contains(t, text, "the real bucket",
		"and it has to say why, or the only available fix is to delete the rule")
	require.NotContains(t, text, "captured into the inbox",
		"S3 is not a mail provider and the refusal must not claim it is")
	require.Contains(t, text, "af net explain")

	rec := s.waitFor(t, func(r record) bool { return r.Host == "s3.amazonaws.com" })
	require.Equal(t, string(schema.ModeBlock), rec.Mode)
	require.Equal(t, "s3.amazonaws.com", rec.Rule)
	require.False(t, rec.Allowed)
}

func TestCloud_AnSQSSendMessageIsRefusedByARuleThatNamesSQS(t *testing.T) {
	s := newSidecar(t, cloudEgress(t))

	// The query API form a v2 client sends, at the regional endpoint, which is
	// the shape no leading wildcard can name without naming the whole cloud.
	send, err := http.NewRequest(http.MethodPost,
		"http://sqs.eu-west-1.amazonaws.com/123456789012/orders",
		strings.NewReader("Action=SendMessage&MessageBody=order-created"))
	require.NoError(t, err)
	send.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := through(t, s, send)

	require.Equal(t, http.StatusForbidden, resp.StatusCode,
		"a message on the real queue is picked up by production workers")
	text := body(t, resp)
	require.Contains(t, text, "sqs.*.amazonaws.com")
	require.Contains(t, text, "production workers")

	rec := s.waitFor(t, func(r record) bool { return r.Host == "sqs.eu-west-1.amazonaws.com" })
	require.Equal(t, "sqs.*.amazonaws.com", rec.Rule)
	require.False(t, rec.Allowed)
}

func TestCloud_MailIsStillCapturedAndOnlyMailIs(t *testing.T) {
	// The control. Every refusal above would also be produced by a policy that
	// refused everything, and a suite of those proves nothing. SES still
	// reaches the inbox, through the same engine and the same catalog.
	s := newSidecar(t, cloudEgress(t))

	send, err := http.NewRequest(http.MethodPost,
		"http://email.us-east-1.amazonaws.com/v2/email/outbound-emails",
		strings.NewReader(`{"FromEmailAddress":"hello@shopfront.test",
			"Destination":{"ToAddresses":["buyer@example.test"]},
			"Content":{"Simple":{"Subject":{"Data":"Your receipt"},
			"Body":{"Text":{"Data":"Your code is 481920"}}}}}`))
	require.NoError(t, err)
	resp := through(t, s, send)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, body(t, resp), "MessageId",
		"an application that stores the message id would otherwise store nothing")

	msg := s.waitForMessage(t)
	require.Equal(t, "ses", msg.Provider)
	require.Equal(t, []string{"buyer@example.test"}, msg.To)
	require.Equal(t, "Your receipt", msg.Subject)
	require.Equal(t, "481920", msg.Code,
		"the one time code is the reason capture exists")
}

func TestCapture_RefusesAHostTheRuleOnlySweptIn(t *testing.T) {
	// The old catalog entry, written out by hand so that the sidecar's half of
	// the fix is proved on its own. Even given exactly the rule that caused
	// the defect, the sidecar no longer invents a success for a service nobody
	// named.
	s := newSidecar(t, &schema.Egress{
		Default: schema.ModeBlock,
		Rules: []schema.EgressRule{{
			Host: "*.amazonaws.com", Mode: schema.ModeCapture,
			Note: "Mail is captured into the inbox.",
		}},
	})

	put, err := http.NewRequest(http.MethodPut,
		"http://s3.amazonaws.com/shopfront-uploads/invoice.pdf", strings.NewReader("%PDF-1.7"))
	require.NoError(t, err)
	resp := through(t, s, put)

	require.Equal(t, http.StatusForbidden, resp.StatusCode,
		"200 with an empty object is a success the application believes")
	text := body(t, resp)
	require.Contains(t, text, "*.amazonaws.com", "the refusal names the rule that decided")
	require.Contains(t, text, "no capture handler")
	require.Contains(t, text, "s3.amazonaws.com")
	require.Contains(t, text, "af net explain")

	rec := s.waitFor(t, func(r record) bool { return r.Event == "decision" && r.Host == "s3.amazonaws.com" })
	require.Equal(t, http.StatusForbidden, rec.Status,
		"the decision log has to agree with the response the application received")
	for _, r := range s.decisions() {
		require.NotEqual(t, "message", r.Event,
			"a refused request must not appear in the inbox as a message that arrived")
	}
}

func TestCapture_RefusesWhenTheDefaultChoseCaptureAndNobodyNamedAnything(t *testing.T) {
	// default: capture is a legal manifest, and it names no host at all. The
	// wording has to be right for it too, because a refusal that talks about
	// a rule when no rule exists sends a reader looking for one.
	s := newSidecar(t, &schema.Egress{Default: schema.ModeCapture})

	put, err := http.NewRequest(http.MethodPut,
		"http://storage.googleapis.com/shopfront/invoice.pdf", strings.NewReader("%PDF-1.7"))
	require.NoError(t, err)
	resp := through(t, s, put)

	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	text := body(t, resp)
	require.Contains(t, text, "The default is capture, which names no host at all")
	require.NotContains(t, text, "The rule for  ",
		"there is no rule here and the refusal must not leave a hole where one would go")
}

func TestCapture_StillAnswersForAHostSomebodyNamed(t *testing.T) {
	// The other half, and the reason the rule is the opt in rather than the
	// handler list. A mail provider this build has never heard of is captured
	// generically when a person wrote its host down, because that is somebody
	// asking for exactly this.
	s := newSidecar(t, &schema.Egress{
		Default: schema.ModeBlock,
		Rules: []schema.EgressRule{{
			Host: "mail.in-house.test", Mode: schema.ModeCapture,
		}},
	})

	send, err := http.NewRequest(http.MethodPost, "http://mail.in-house.test/send",
		strings.NewReader(`{"to":"buyer@example.test","body":"code 481920"}`))
	require.NoError(t, err)
	resp := through(t, s, send)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	msg := s.waitForMessage(t)
	require.Equal(t, "unknown", msg.Provider)
	require.Contains(t, msg.Text, "buyer@example.test")
}

func TestCapture_SlackAnswersTheShapeItsClientChecks(t *testing.T) {
	// The catalog has always set Slack to capture, and the generic handler
	// answered it with an empty object. Every Slack client reads ok before
	// anything else, so a captured message read as a failed one.
	s := newSidecar(t, &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: "slack.com", Mode: schema.ModeCapture}},
	})

	post, err := http.NewRequest(http.MethodPost, "http://slack.com/api/chat.postMessage",
		strings.NewReader(`{"channel":"#orders","text":"an order was placed"}`))
	require.NoError(t, err)
	resp := through(t, s, post)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, body(t, resp), `"ok":true`)
	msg := s.waitForMessage(t)
	require.Equal(t, "slack", msg.Provider)
	require.Equal(t, "an order was placed", msg.Text)
}
