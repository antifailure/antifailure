package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The DNS server is what makes the policy apply to every client rather than to
// the well behaved ones.
//
// Proxy environment variables are a request. Node ignores them entirely, Go's
// http.Client honours them only through a transport that opts in, and a great
// many SDKs bundle their own client that does neither. An egress control that
// only works for clients that agreed to it is not a control.
//
// So every name a service looks up that is not inside the environment resolves
// to the sidecar's own address, and the sidecar accepts the connection that
// follows. The client believes it is talking to the origin; the policy decides
// whether it ever will be. Names that are inside the environment are forwarded
// to Docker's own resolver unchanged, because a service calling another
// service is not egress and must not become a policy decision.
//
// This is hand written rather than taken from a library because the sidecar's
// image is built from source carried in the engine binary, with no module
// downloads, so everything it uses has to be the standard library.

// dnsServer answers lookups for the environment.
type dnsServer struct {
	// self is the address every external name resolves to.
	self net.IP
	// internal are the names that must resolve normally: other services, the
	// database, and the sidecar itself.
	internal map[string]bool
	// upstream is Docker's embedded resolver, which knows the environment's
	// own names.
	upstream string
	emit     func(record)

	mu     sync.Mutex
	logged map[string]bool
}

func newDNSServer(self net.IP, internal []string, upstream string, emit func(record)) *dnsServer {
	set := map[string]bool{}
	for _, n := range internal {
		if n = strings.ToLower(strings.TrimSpace(n)); n != "" {
			set[n] = true
		}
	}
	return &dnsServer{self: self, internal: set, upstream: upstream, emit: emit, logged: map[string]bool{}}
}

// serve answers queries until the connection fails.
func (d *dnsServer) serve(pc net.PacketConn) error {
	buf := make([]byte, 1500)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return err
		}
		query := make([]byte, n)
		copy(query, buf[:n])
		go func() {
			resp := d.answer(query)
			if resp != nil {
				_, _ = pc.WriteTo(resp, addr)
			}
		}()
	}
}

// answer builds a reply for one query.
func (d *dnsServer) answer(query []byte) []byte {
	name, qtype, err := parseQuestion(query)
	if err != nil {
		return nil
	}
	bare := strings.TrimSuffix(strings.ToLower(name), ".")

	if bare == "localhost" || strings.HasSuffix(bare, ".localhost") {
		// Answered here rather than forwarded. RFC 6761 reserves the name for
		// the loopback interface and says a resolver must not send it to the
		// network, and forwarding it was a way out with a payload in it: a
		// query for a-secret-value.attacker.example.localhost went to the
		// upstream resolver like any other name.
		if qtype == typeA {
			return replyA(query, name, net.IPv4(127, 0, 0, 1))
		}
		return emptyAnswer(query)
	}

	if d.isInternal(bare) {
		// Forwarded unchanged. A service calling another service, or the
		// database, is not egress, and routing it through the sidecar would
		// turn every internal call into a policy decision and every internal
		// failure into a confusing one.
		if resp, ferr := d.forward(query); ferr == nil {
			return resp
		}
		return refused(query)
	}

	switch qtype {
	case typeA:
		d.note(bare)
		return replyA(query, name, d.self)
	case typeAAAA:
		// An empty answer rather than a refusal, so a client that asks for
		// both falls back to the address above instead of failing outright.
		// Handing back an IPv6 address the sidecar does not listen on would
		// produce a connection that hangs instead of one that is decided.
		return emptyAnswer(query)
	default:
		// Answered here rather than forwarded, and this is the hole that
		// closed. Forwarding meant a TXT, NULL, CNAME or SRV query for an
		// external name went to whatever resolves names for this host, which
		// is a channel out of the environment carrying whatever the client
		// put in the name, on a port and a protocol the policy never sees. It
		// is the oldest exfiltration route there is, and the Kubernetes
		// containment probe already counts a query to another resolver as an
		// escape.
		//
		// An empty answer rather than a refusal, for the same reason the AAAA
		// case gives one: a client that asks for several types and gets a
		// refusal for one often gives up on the name entirely.
		d.noteTunnel(bare, qtype)
		return emptyAnswer(query)
	}
}

func (d *dnsServer) isInternal(name string) bool {
	if d.internal[name] {
		return true
	}
	// A single label with no dot is a container name or an alias on one of
	// the environment's networks. Nothing outside is addressed that way.
	if !strings.Contains(name, ".") {
		return true
	}
	return strings.HasSuffix(name, ".localhost") || name == "localhost"
}

// note records the first time a name is intercepted, so the decision log shows
// what the environment looked up as well as what it connected to. A name that
// resolves and is never connected to is worth seeing.
func (d *dnsServer) note(name string) {
	d.mu.Lock()
	seen := d.logged[name]
	d.logged[name] = true
	d.mu.Unlock()
	if !seen && d.emit != nil {
		d.emit(record{Event: "resolve", Host: name})
	}
}

// noteTunnel records an external lookup for a record type this resolver does
// not answer, once per name.
//
// Recorded rather than dropped silently, because the interesting thing about
// a burst of TXT queries for subdomains of one name is that it happened at
// all, and af net log is where somebody would look for it.
func (d *dnsServer) noteTunnel(name string, qtype uint16) {
	d.mu.Lock()
	key := name + "/" + strconv.Itoa(int(qtype))
	seen := d.logged[key]
	d.logged[key] = true
	d.mu.Unlock()
	if seen || d.emit == nil {
		return
	}
	d.emit(record{
		Event: "resolve", Host: name, Allowed: false,
		Reason: "This environment answers only address lookups for external names. A " +
			recordTypeName(qtype) + " query is a way to carry data out of the environment " +
			"without opening a connection, so it is answered here rather than forwarded.",
	})
}

// recordTypeName names the types worth naming, for the log line above.
func recordTypeName(qtype uint16) string {
	switch qtype {
	case 5:
		return "CNAME"
	case 10:
		return "NULL"
	case 12:
		return "PTR"
	case 15:
		return "MX"
	case 16:
		return "TXT"
	case 33:
		return "SRV"
	case 255:
		return "ANY"
	}
	return "type " + strconv.Itoa(int(qtype))
}

func (d *dnsServer) forward(query []byte) ([]byte, error) {
	conn, err := net.DialTimeout("udp", d.upstream, 3*time.Second)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write(query); err != nil {
		return nil, err
	}
	buf := make([]byte, 1500)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// Wire format constants. Only what is needed to answer an A query.
const (
	typeA    = 1
	typeAAAA = 28
	classIN  = 1

	headerLen = 12
	// flagResponse marks a reply; flagRA says recursion is available, which a
	// resolver that answers everything must claim or some clients retry.
	flagResponse      = 0x8000
	flagRecursionDesr = 0x0100
	flagRA            = 0x0080
	rcodeRefused      = 5
)

var errMalformed = errors.New("malformed query")

// parseQuestion reads the single question every real query carries.
func parseQuestion(msg []byte) (string, uint16, error) {
	if len(msg) < headerLen {
		return "", 0, errMalformed
	}
	if qdcount := binary.BigEndian.Uint16(msg[4:6]); qdcount != 1 {
		// Every resolver in practice sends exactly one question, and the
		// format allows more only in theory. Refusing the theoretical case is
		// better than mis-parsing it.
		return "", 0, errMalformed
	}
	name, off, err := readName(msg, headerLen)
	if err != nil {
		return "", 0, err
	}
	if off+4 > len(msg) {
		return "", 0, errMalformed
	}
	return name, binary.BigEndian.Uint16(msg[off : off+2]), nil
}

// readName decodes a domain name, refusing compression pointers.
//
// A question section never uses them, and following one is where a DNS parser
// gets an unbounded loop from a hostile packet.
func readName(msg []byte, off int) (string, int, error) {
	var b strings.Builder
	for {
		if off >= len(msg) {
			return "", 0, errMalformed
		}
		n := int(msg[off])
		off++
		if n == 0 {
			return b.String(), off, nil
		}
		if n&0xC0 != 0 {
			return "", 0, errMalformed
		}
		if off+n > len(msg) {
			return "", 0, errMalformed
		}
		b.Write(msg[off : off+n])
		b.WriteByte('.')
		off += n
	}
}

// questionEnd returns the offset just past the question section.
func questionEnd(msg []byte) (int, error) {
	_, off, err := readName(msg, headerLen)
	if err != nil {
		return 0, err
	}
	if off+4 > len(msg) {
		return 0, errMalformed
	}
	return off + 4, nil
}

// replyA answers with one address and a short lifetime.
//
// The lifetime is deliberately short: a policy edit takes effect on the next
// lookup, and a client that cached an answer for an hour would keep talking to
// a sidecar decision that no longer exists.
func replyA(query []byte, name string, ip net.IP) []byte {
	end, err := questionEnd(query)
	if err != nil {
		return nil
	}
	v4 := ip.To4()
	if v4 == nil {
		return emptyAnswer(query)
	}

	out := make([]byte, 0, end+16)
	out = append(out, query[:end]...)
	binary.BigEndian.PutUint16(out[2:4], flagResponse|flagRA|(binary.BigEndian.Uint16(query[2:4])&flagRecursionDesr))
	binary.BigEndian.PutUint16(out[6:8], 1) // one answer
	binary.BigEndian.PutUint16(out[8:10], 0)
	binary.BigEndian.PutUint16(out[10:12], 0)

	// The answer repeats the question's name rather than compressing it,
	// which is a handful of extra bytes and one less thing to get wrong.
	out = appendName(out, name)
	out = binary.BigEndian.AppendUint16(out, typeA)
	out = binary.BigEndian.AppendUint16(out, classIN)
	out = binary.BigEndian.AppendUint32(out, 5) // five second lifetime
	out = binary.BigEndian.AppendUint16(out, 4)
	out = append(out, v4...)
	return out
}

func appendName(out []byte, name string) []byte {
	for _, label := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		if label == "" {
			continue
		}
		if len(label) > 63 {
			label = label[:63]
		}
		out = append(out, byte(len(label)))
		out = append(out, label...)
	}
	return append(out, 0)
}

func emptyAnswer(query []byte) []byte {
	end, err := questionEnd(query)
	if err != nil {
		return nil
	}
	out := make([]byte, end)
	copy(out, query[:end])
	binary.BigEndian.PutUint16(out[2:4], flagResponse|flagRA)
	binary.BigEndian.PutUint16(out[6:8], 0)
	binary.BigEndian.PutUint16(out[8:10], 0)
	binary.BigEndian.PutUint16(out[10:12], 0)
	return out
}

func refused(query []byte) []byte {
	out := emptyAnswer(query)
	if out == nil {
		return nil
	}
	binary.BigEndian.PutUint16(out[2:4], flagResponse|flagRA|rcodeRefused)
	return out
}

// dockerResolver is where Docker's embedded DNS listens inside a container.
//
// It is the default rather than the only answer. The sidecar forwards internal
// names to whatever already resolves them, and that is Docker's embedded
// resolver on a container runtime and the cluster's own resolver on
// Kubernetes, where 127.0.0.11 is nothing at all. Hardcoding it here made
// every service name in a cluster fail to resolve, which reads as an
// environment whose services cannot find each other rather than as a DNS
// setting.
const dockerResolver = "127.0.0.11:53"

func describeDNS(self net.IP, internal []string) string {
	return fmt.Sprintf("dns answering %s for every external name, forwarding %s",
		self, strings.Join(internal, ", "))
}
