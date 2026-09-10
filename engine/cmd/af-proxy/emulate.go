package main

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/policy"
)

// Emulate mode answers from an emulator running inside the environment.
//
// This file is routing and nothing else. Antifailure writes no emulators:
// LocalStack, Azurite and the vendors' own carry years of fidelity work a hand
// written replacement would not have, and nobody buys this product because its
// S3 emulator is good. What every one of those emulators costs you is a change
// to the application, an endpoint override or a client construction that only
// exists in tests, and an application changed for the test is not the
// application that ships. The change is small enough that everybody makes it
// and large enough that it moves the code under test off the path that runs in
// production.
//
// So the request is not redirected, it is answered. The name still resolves to
// the sidecar, the sidecar still presents a certificate for the provider's own
// hostname signed by the authority the environment already trusts, and the
// body is forwarded to a container on the environment's own network. The
// application's SDK is configured for production and stays that way.
//
// TWO THINGS ARE DELIBERATELY NOT REWRITTEN, and both would look like
// tidiness.
//
// The Host header keeps the name the application asked for. Rewriting it to
// the emulator's address is the obvious move and it breaks the case this mode
// exists for: virtual hosted addressing puts the S3 bucket in the hostname, so
// mybucket.s3.us-east-1.amazonaws.com IS the request, and an emulator handed
// Host: af-emu-localstack:4566 has been told the bucket is called af-emu.
// LocalStack, Azurite and fake-gcs-server all read the header. So the
// destination changes and the request does not.
//
// The Authorization header is forwarded untouched. Sandbox mode replaces a
// credential because the request leaves the environment and a real provider is
// on the other end; here the other end is a container on a network Docker
// created with internal set, which has no route out at all, so there is
// nothing for a credential to leak to. Replacing it would also break an
// emulator that parses the key id out of a SigV4 header, which several do in
// order to key their state by account. What the sidecar does instead is what
// the shared conventions ask for: it RECORDS the key id and REFUSES a key
// livekey says is live, which the tripwire already does on all three paths
// before this function is reached.

// emulatorRoute is where one emulator answers, on the environment's own
// network.
//
// Written by the engine into the sidecar's configuration rather than named by
// a rule. A rule names an emulator and never an address, so a manifest cannot
// point traffic at a host of its choosing: the address here came from a
// registration whose image digest and host list the registry already checked.
type emulatorRoute struct {
	// Address is host:port on the environment's inner network.
	Address string `json:"address"`
}

// serveEmulated forwards one request to an emulator and writes the response.
//
// It takes an io.Writer rather than an http.ResponseWriter because two of the
// three paths that reach it hold a raw connection, and a second implementation
// of this for the third path is how the modes came to behave differently
// depending on which HTTP library the application used.
func (p *proxy) serveEmulated(
	w io.Writer, req *http.Request, host string, d policy.Decision, rec *record,
) {
	rec.Emulator = d.Emulator
	rec.KeyID = accessKeyID(req.Header.Get("Authorization"))

	route, ok := p.emulators[d.Emulator]
	if !ok {
		// Refused rather than forwarded somewhere plausible. The rule says
		// this host is answered by an emulator, and answering it any other
		// way would be the environment quietly testing against something
		// nobody named.
		rec.Status = http.StatusBadGateway
		rec.Allowed = false
		rec.Reason = fmt.Sprintf(
			"The rule for %s names the emulator %q, and this environment is running none by that name.",
			d.RuleHost, d.Emulator)
		writeRawStatus(w, http.StatusBadGateway, "text/plain; charset=utf-8",
			missingEmulatorBody(host, req, d, p.emulatorNames()))
		return
	}

	outbound := req.Clone(req.Context())
	outbound.RequestURI = ""
	// http, not https. The emulator is one hop away on a network with no
	// route out, both ends of that hop are inside the environment, and a
	// second TLS termination inside one environment would buy a certificate
	// to protect a wire nothing else can reach.
	outbound.URL.Scheme = "http"
	outbound.URL.Host = route.Address
	// The one line that makes an unmodified application work. req.Clone
	// carries Host across, and it is set again explicitly because it is the
	// field a future edit is most likely to overwrite with URL.Host without
	// noticing that virtual hosted addressing lives in it.
	outbound.Host = host
	for _, h := range hopByHop {
		outbound.Header.Del(h)
	}
	// Recorded on the way through, so a reader of the decision log can tell
	// an emulated call from a real one without reading the manifest.
	outbound.Header.Set("X-Antifailure-Emulated", d.Emulator)

	resp, err := p.transport.RoundTrip(outbound)
	if err != nil {
		rec.Error = err.Error()
		rec.Status = http.StatusBadGateway
		writeRawStatus(w, http.StatusBadGateway, "text/plain; charset=utf-8",
			fmt.Sprintf("Antifailure could not reach the %s emulator at %s: %s\n",
				d.Emulator, route.Address, err.Error()))
		return
	}
	defer func() { _ = resp.Body.Close() }()

	rec.Status = resp.StatusCode
	// Written with Write rather than by hand, so chunked encoding, trailers
	// and connection semantics are the standard library's problem. An
	// emulator's response is an API response somebody's SDK is about to
	// parse, and a corrupted one fails three steps later as an application
	// bug.
	if err := resp.Write(w); err != nil {
		rec.Error = err.Error()
		return
	}
	rec.Bytes = resp.ContentLength
	if rec.Bytes < 0 {
		rec.Bytes = 0
	}
}

// emulatorNames lists what this environment is running, sorted.
func (p *proxy) emulatorNames() []string {
	out := make([]string, 0, len(p.emulators))
	for name := range p.emulators {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// missingEmulatorBody is what a developer reads when a rule names an emulator
// the environment does not have.
//
// It names what IS running, because the two ways to arrive here are a typo and
// a build with nothing registered, and the list tells them apart at a glance.
func missingEmulatorBody(
	host string, req *http.Request, d policy.Decision, running []string,
) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Antifailure could not answer this request.\n\n")
	fmt.Fprintf(&b, "  %s https://%s%s\n\n", req.Method, host, req.URL.Path)
	fmt.Fprintf(&b, "The rule for %s is set to emulate and names the emulator %q.\n",
		d.RuleHost, d.Emulator)
	if len(running) == 0 {
		fmt.Fprintf(&b, "This environment is running no emulators at all.\n\n")
		fmt.Fprintf(&b, "An emulator is supplied by a registration rather than by the manifest, so a\n")
		fmt.Fprintf(&b, "build with none registered has nothing an emulate rule can reach. Nothing was\n")
		fmt.Fprintf(&b, "sent and nothing was invented.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "This environment is running: %s.\n\n", strings.Join(running, ", "))
	fmt.Fprintf(&b, "Name one of those, or register the one you meant. Nothing was sent.\n")
	return b.String()
}

// accessKeyID returns the key identifier out of an Authorization header, and
// never the secret.
//
// Recorded because the shared conventions ask for it: an emulator verifies no
// signature, so the only thing standing between a misconfigured application
// and a real cloud account is the tripwire, and the tripwire's refusals are
// only auditable if the accepted requests say which key they carried. The key
// id is public by construction in every scheme below; the secret never appears
// in a header at all.
//
// SigV4 puts it in Credential=<id>/<date>/<region>/<service>/aws4_request.
// Azure's shared key scheme puts the account name after the scheme. Anything
// else returns empty rather than guessing, because a wrong id in an audit log
// is worse than none.
func accessKeyID(authorization string) string {
	if authorization == "" {
		return ""
	}
	if strings.HasPrefix(authorization, "AWS4-HMAC-SHA256 ") {
		for _, part := range strings.Split(authorization[len("AWS4-HMAC-SHA256 "):], ",") {
			part = strings.TrimSpace(part)
			if !strings.HasPrefix(part, "Credential=") {
				continue
			}
			scope := strings.TrimPrefix(part, "Credential=")
			if slash := strings.Index(scope, "/"); slash >= 0 {
				return scope[:slash]
			}
			return scope
		}
		return ""
	}
	// SharedKey <account>:<signature> and SharedKeyLite <account>:<signature>.
	// The signature is after the colon and is deliberately not returned.
	for _, scheme := range []string{"SharedKeyLite ", "SharedKey "} {
		if !strings.HasPrefix(authorization, scheme) {
			continue
		}
		rest := authorization[len(scheme):]
		if colon := strings.Index(rest, ":"); colon >= 0 {
			return rest[:colon]
		}
		return rest
	}
	return ""
}

// insideBodyLimit bounds a request this sidecar answers itself.
//
// It exists because answering a request means holding its body in memory, and
// the sidecar is one process serving every service in the environment.
const insideBodyLimit = 1 << 20

// oversizedEmulateReason is the one line the decision log gets.
const oversizedEmulateReason = "This request is larger than the sidecar will hold in memory " +
	"on the plain proxy port, and an emulated request cannot be truncated: the emulator " +
	"would store a short object and answer as though it had stored the whole one."

// oversizedEmulateBody is what a developer reads, and it names the way out.
//
// The way out is real rather than a shrug. Every cloud SDK speaks HTTPS, and
// the inspected path streams the body straight through with no limit at all,
// so an application reaching this has been configured to talk to a cloud API
// over plain HTTP, which is a thing worth telling somebody about on its own.
func oversizedEmulateBody(host string, req *http.Request) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Antifailure refused this request rather than truncating it.\n\n")
	fmt.Fprintf(&b, "  %s http://%s%s\n\n", req.Method, host, req.URL.Path)
	fmt.Fprintf(&b, "The body is larger than %d bytes, and this request arrived over plain\n",
		insideBodyLimit)
	fmt.Fprintf(&b, "HTTP through the proxy port, where the sidecar has to hold the whole body\n")
	fmt.Fprintf(&b, "in memory before it can answer. Cutting it short would leave the emulator\n")
	fmt.Fprintf(&b, "holding a short object behind a success, which is the one outcome worse\n")
	fmt.Fprintf(&b, "than a refusal because it is believed.\n\n")
	fmt.Fprintf(&b, "Use https for %s. Every cloud SDK does by default, the sidecar terminates\n", host)
	fmt.Fprintf(&b, "it with the certificate this environment already trusts, and that path\n")
	fmt.Fprintf(&b, "streams the body through with no limit.\n")
	return b.String()
}
