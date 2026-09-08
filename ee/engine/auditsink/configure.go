package auditsink

// Reading the sinks an installation has actually configured, and plugging them
// in.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// The rule is the one AF_SECRET_SOURCES and AF_ORG_POLICY_FILE already follow,
// and it is sharper here than in either.
//
// Somebody who writes AF_AUDIT_SINKS=syslog has said that every privileged
// action this engine takes must be forwarded to their SIEM. Starting anyway
// with the sink unbuilt means every action goes unforwarded and nothing in the
// output says so, which is indistinguishable from a quiet week. That is not a
// degraded feature, it is a compliance control that reports itself as held
// while holding nothing, and it is the exact defect this whole lane exists to
// close. So a named sink that cannot be built stops the process with the reason.
//
// An unset variable registers nothing and prints nothing, which is the ordinary
// case. Nothing is ever auto-detected: a machine that happens to carry AWS
// credentials for something unrelated must not have this tool decide on its own
// to start writing that organization's audit trail into their bucket.

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// SinksEnv names the variable that lists which sinks to use.
const SinksEnv = "AF_AUDIT_SINKS"

// The variables each sink reads. Named as constants rather than written inline
// so that the error message listing what is missing and the code reading it
// cannot drift apart.
const (
	SyslogAddressEnv  = "AF_AUDIT_SYSLOG_ADDRESS"
	SyslogCAEnv       = "AF_AUDIT_SYSLOG_CA_FILE"
	SyslogCertEnv     = "AF_AUDIT_SYSLOG_CERT_FILE"
	SyslogKeyEnv      = "AF_AUDIT_SYSLOG_KEY_FILE"
	SyslogHostnameEnv = "AF_AUDIT_SYSLOG_HOSTNAME"

	WebhookURLEnv        = "AF_AUDIT_WEBHOOK_URL"
	WebhookSecretEnv     = "AF_AUDIT_WEBHOOK_SECRET"
	WebhookHeaderEnv     = "AF_AUDIT_WEBHOOK_HEADER"
	WebhookDeadLetterEnv = "AF_AUDIT_WEBHOOK_DEAD_LETTER_FILE"

	ObjectStoreURLEnv = "AF_AUDIT_OBJECT_STORE_URL"
)

// Known are the sinks this build can write to, for the message that lists them
// when somebody names one that does not exist.
func Known() []string {
	out := []string{"syslog", "webhook", "object_store"}
	sort.Strings(out)
	return out
}

// Sink is what this package registers: the interface, plus the name, so a
// caller can print what was configured without knowing which concrete type it
// got.
type Sink = extension.AuditSink

// FromEnvironment builds the sinks named by AF_AUDIT_SINKS, in order.
//
// The order is the order given and it is the order they are written in, so an
// installation that wants its SIEM to see an entry before its archive does can
// say so. Every sink is written to regardless of whether an earlier one failed:
// see Registry.Audit, which collects failures rather than stopping, because a
// SIEM outage must not cost the archive its copy.
func FromEnvironment(getenv func(string) string) ([]Sink, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	names := strings.FieldsFunc(getenv(SinksEnv), func(r rune) bool {
		return r == ',' || r == ' '
	})
	if len(names) == 0 {
		return nil, nil
	}

	out := make([]Sink, 0, len(names))
	for _, raw := range names {
		name := strings.ToLower(strings.TrimSpace(raw))
		sink, err := build(name, getenv)
		if err != nil {
			return nil, fmt.Errorf("%s names %s and it cannot be used: %w", SinksEnv, name, err)
		}
		out = append(out, sink)
	}
	return out, nil
}

func build(name string, getenv func(string) string) (Sink, error) {
	switch name {
	case "syslog":
		return NewSyslog(SyslogConfig{
			Address:  getenv(SyslogAddressEnv),
			CAFile:   getenv(SyslogCAEnv),
			CertFile: getenv(SyslogCertEnv),
			KeyFile:  getenv(SyslogKeyEnv),
			Hostname: getenv(SyslogHostnameEnv),
		})
	case "webhook":
		return NewWebhook(WebhookConfig{
			URL:            getenv(WebhookURLEnv),
			Secret:         getenv(WebhookSecretEnv),
			Header:         getenv(WebhookHeaderEnv),
			DeadLetterFile: getenv(WebhookDeadLetterEnv),
		})
	case "object_store", "objectstore", "s3", "azure_blob":
		// Four spellings for one sink, because the URL is what decides which
		// protocol is spoken and somebody who writes s3 means this. Refusing
		// the obvious name and naming the canonical one in the error would be a
		// round trip for nothing.
		return NewObjectStore(ObjectStoreConfig{
			URL:    getenv(ObjectStoreURLEnv),
			Getenv: getenv,
		})
	default:
		return nil, fmt.Errorf("there is no such sink in this build; it knows %s",
			strings.Join(Known(), ", "))
	}
}

// RegisterFromEnvironment reads the configured sinks and plugs them in.
//
// The one call an embedding binary makes. It returns the sinks so the binary
// can print which destinations are in force, and so that a binary that forgot
// to print them still cannot forget to register them: there is no way to get a
// sink out of here without it already being in the registry. That property is
// what was missing before this lane, in exactly this shape: policyenforce.Hook
// was written, tested and never constructed by any binary, and audit_stream had
// no implementation to construct at all.
func RegisterFromEnvironment(reg *extension.Registry, getenv func(string) string) ([]Sink, error) {
	sinks, err := FromEnvironment(getenv)
	if err != nil {
		return nil, err
	}
	for _, s := range sinks {
		reg.AddAuditSink(s)
	}
	return sinks, nil
}

// Describe names each configured sink, for a startup line and for af doctor.
//
// Worth printing for the same reason the secret sources are: an operator asking
// why an action did not appear in their SIEM needs to know whether this
// installation was forwarding at all before they go looking at the receiver.
// Each sink's Name is already written to carry no credential.
func Describe(sinks []Sink) []string {
	out := make([]string, 0, len(sinks))
	for _, s := range sinks {
		out = append(out, s.Name())
	}
	return out
}

// Unlicensed reports whether entries will actually be forwarded.
//
// Configured and licensed are two different things and an operator needs to be
// able to tell them apart. A sink that is configured under a lapsed licence
// accepts every entry and writes none, which is the correct behaviour and is
// also silence, so the binary says so once at startup rather than leaving
// somebody to discover it from an empty dashboard.
func Unlicensed(ctx context.Context, sinks []Sink) bool {
	return len(sinks) > 0 && !permitted(ctx)
}
