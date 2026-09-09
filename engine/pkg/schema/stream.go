package schema

import (
	"net"
	"sort"
	"strconv"
)

// The ports an environment answers on for protocols that are not HTTP.
//
// This table lives in the schema package rather than beside the sidecar that
// listens on it because THREE things have to agree about it and they run in
// three different processes. The sidecar opens a listener per port. The
// Kubernetes runtime writes a NetworkPolicy, and a port missing there is a
// packet the cluster drops before the sidecar ever sees it, which presents as
// a hang rather than as a refusal. The manifest validator refuses the modes
// that cannot be honoured on these ports, and it has to know which ports
// those are before anything runs.
//
// Three copies of one list is how the three drift, and the one that drifted
// would be the one deciding real traffic. So there is one, here, and the tests
// that matter compare each consumer against it rather than against a literal.

// StreamProtocol is one port the sidecar answers on for a protocol that is not
// HTTP.
//
// The name is not used to parse anything. It exists so that a refusal can say
// which protocol the developer was probably speaking, because the alternative
// is a log line that says port 5671 to somebody who is looking at an Azure
// Service Bus stack trace.
type StreamProtocol struct {
	// Port is the TCP port this listener accepts on.
	Port int
	// Name is what a developer calls the protocol.
	Name string
	// SNI records whether the protocol's usual managed form wraps itself in
	// TLS from the first byte, which is what makes the host readable.
	//
	// It changes no behaviour: every port here is decided by looking for a
	// ClientHello, and a broker running amqps on a port this table calls
	// cleartext is decided correctly anyway. It is here so the refusal can
	// say whether the developer is likely to have a working alternative, and
	// so the measurement table has a column that does not have to be
	// maintained by hand beside the code.
	SNI bool
}

// ByteStreamProtocols are the ports the sidecar answers on beyond HTTP.
//
// Chosen by asking, for each protocol, what a managed provider a real
// application actually pays for listens on: Azure Service Bus and CloudAMQP on
// 5671, Confluent Cloud and MSK on 9093, MongoDB Atlas on 27017, Azure Cache
// for Redis on 6380. The cleartext ports beside them are here so that a
// self-hosted broker gets a readable refusal naming the TLS port instead of a
// connection reset that reads as a network fault.
//
// It is a fixed list rather than everything, because a listener per port for
// all 65535 of them is not a design, and because a port nobody named is a port
// whose refusal nobody will read. Ports named in the manifest's own rules are
// added to this set at startup, so a broker on an unusual port is reachable by
// declaring it.
var ByteStreamProtocols = []StreamProtocol{
	{Port: 22, Name: "SSH", SNI: false},
	{Port: 25, Name: "SMTP", SNI: false},
	{Port: 465, Name: "SMTP over TLS", SNI: true},
	{Port: 587, Name: "SMTP submission", SNI: false},
	{Port: 636, Name: "LDAP over TLS", SNI: true},
	{Port: 3306, Name: "MySQL", SNI: false},
	{Port: 4222, Name: "NATS", SNI: false},
	{Port: 5432, Name: "PostgreSQL", SNI: false},
	{Port: 5671, Name: "AMQP over TLS", SNI: true},
	{Port: 5672, Name: "AMQP", SNI: false},
	{Port: 6379, Name: "Redis", SNI: false},
	{Port: 6380, Name: "Redis over TLS", SNI: true},
	{Port: 8883, Name: "MQTT over TLS", SNI: true},
	{Port: 9092, Name: "Kafka", SNI: false},
	{Port: 9093, Name: "Kafka over TLS", SNI: true},
	{Port: 27017, Name: "MongoDB", SNI: true},
}

// StreamPorts is the set of ports the byte stream path listens on for a given
// policy: the table above, plus any port the policy's own rules name.
//
// The rules are consulted so that a broker on a port nobody standardised is
// reachable by writing it down, which is the same bargain the rest of the
// manifest makes. Ports the HTTP listeners already own are excluded, because
// two listeners on one port is a startup failure and because a request on 80
// or 443 is one this sidecar can read properly.
func StreamPorts(rules []EgressRule) []StreamProtocol {
	known := map[int]StreamProtocol{}
	for _, p := range ByteStreamProtocols {
		known[p.Port] = p
	}
	// Seeded from the rules rather than from the table, because the manifest
	// is the authority on what an environment may reach and the table is only
	// a list of names for ports. Seeding the table made every environment
	// answer on sixteen ports nobody asked for, and an accepted connection
	// here is forwarded on the strength of the name in its handshake: a rule
	// that spells no port matches every port, so a manifest naming a host for
	// HTTP also carried that host's Redis and its SMTP.
	byPort := map[int]StreamProtocol{}
	for _, r := range rules {
		_, portText, err := net.SplitHostPort(r.Host)
		if err != nil {
			continue
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port <= 0 || port > 65535 {
			continue
		}
		if p, isKnown := known[port]; isKnown {
			byPort[port] = p
			continue
		}
		byPort[port] = StreamProtocol{Port: port, Name: "the protocol on port " + portText}
	}
	// The ports HTTP already answers on. Listening twice on one of these
	// would fail at startup, and the HTTP listener is the better of the two
	// because it can read the request.
	delete(byPort, 80)
	delete(byPort, 443)
	delete(byPort, ProxyListenPort)
	// Port 53 is the resolver's, over TCP as well as UDP for a client that
	// retries a truncated answer.
	delete(byPort, 53)

	out := make([]StreamProtocol, 0, len(byPort))
	for _, p := range byPort {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

// ProxyListenPort is the explicit proxy port, named here so StreamPorts can
// exclude it without importing the runtime that also declares it.
const ProxyListenPort = 3128
