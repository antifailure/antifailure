package auditsink

// Syslog over TLS, which is what a SIEM ingests without being asked twice.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Three specifications and all three are load bearing, so they are named rather
// than implied:
//
//   - RFC 5424 is the message. The old BSD format in RFC 3164 has no year in
//     its timestamp, no sub-second precision, and no defined encoding, which is
//     three ways for an audit record to become unusable at the moment somebody
//     needs it.
//   - RFC 5425 is syslog over TLS, and octet counting framing is what it
//     mandates. The other framing in RFC 6587, a trailing newline, cannot carry
//     a message with a newline in it, and a JSON detail field holding a policy
//     refusal written for a terminal has newlines in it. Framing by length is
//     the difference between one entry and an entry that arrives as four
//     corrupt ones.
//   - The facility is 13, "log audit", from RFC 5424's own table. A receiver
//     routes on facility, so an audit stream arriving as facility 1 lands in
//     the same bucket as everything else a machine says.
//
// # There is no plaintext option and that is deliberate
//
// A plain TCP or UDP syslog sink would be a handful of lines less code and it
// would put the record of who was given a copy of production onto the network
// in the clear, with no authentication of the receiver, which is precisely the
// property the entries themselves exist to prove is not happening. An address
// configured without TLS is refused with a sentence saying so, rather than
// downgraded.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// syslogFacility is "log audit" from RFC 5424 table 1.
const syslogFacility = 13

// syslogSeverity is informational. An audit entry is a record of something
// that worked as designed, including a refusal, so it is not a warning and it
// is certainly not an error: a receiver alerting on severity would page
// somebody every time the product did its job.
const syslogSeverity = 6

// syslogVersion is the RFC 5424 version field, which has been 1 since 2009.
const syslogVersion = 1

// syslogDialTimeout bounds reaching the receiver.
//
// Five seconds, and it is the number that decides whether a SIEM outage is
// invisible or is something a developer waits for. An `af down` forwards one
// entry, so this is the whole of what a dead receiver costs a teardown, once.
const syslogDialTimeout = 5 * time.Second

// syslogWriteTimeout bounds one framed message.
const syslogWriteTimeout = 5 * time.Second

// SyslogConfig is what a TLS syslog sink needs.
type SyslogConfig struct {
	// Address is host:port. Port 6514 is the registered one for syslog over
	// TLS and is assumed when the address carries none.
	Address string
	// CAFile is a PEM bundle the receiver's certificate is verified against.
	// Empty means the system roots, which is right for a hosted SIEM and wrong
	// for the private CA most on-premise collectors use.
	CAFile string
	// CertFile and KeyFile are this client's certificate, for a receiver that
	// authenticates its senders with mutual TLS. Both or neither.
	CertFile string
	KeyFile  string
	// Hostname is what the messages claim to come from. Empty takes the
	// machine's, which is what a receiver correlating by host expects.
	Hostname string
	// AppName is the APP-NAME field, bounded at 48 characters by RFC 5424.
	AppName string
	// Now is injected for tests. Nil means the wall clock.
	Now func() time.Time
	// dial is injected for tests that need a server without a certificate
	// authority. Nil means a real TLS dial, and nothing outside this package
	// can set it, which is what keeps "there is no plaintext option" true.
	dial func(ctx context.Context) (net.Conn, error)
}

// Syslog forwards audit entries to a receiver over TLS.
type Syslog struct {
	cfg SyslogConfig
	tls *tls.Config
	now func() time.Time

	// mu guards the connection. Write is called from the engine's teardown and
	// from its creation path, which are different goroutines in a run that
	// tears one environment down while bringing another up.
	mu   sync.Mutex
	conn net.Conn
}

// NewSyslog builds a TLS syslog sink, or reports what it is missing.
//
// Every failure here is at construction rather than at the first entry, which
// is the same rule the enterprise secret sources follow: an operator whose CA
// file is missing should be told when the binary starts and not twenty minutes
// later, inside a teardown, in a line nobody is watching.
func NewSyslog(cfg SyslogConfig) (*Syslog, error) {
	address := strings.TrimSpace(cfg.Address)
	if address == "" {
		return nil, fmt.Errorf("a syslog sink needs an address, as host:port")
	}
	// A scheme is accepted and checked rather than rejected, because somebody
	// will write one and the useful answer to syslog://collector is the reason
	// this sink does not do that rather than a parse error.
	if scheme, rest, found := strings.Cut(address, "://"); found {
		switch strings.ToLower(scheme) {
		case "syslog+tls", "syslogs", "tls":
			address = rest
		case "syslog", "tcp", "udp":
			return nil, fmt.Errorf(
				"%s names %s, which is syslog in the clear. This sink is TLS only: the entries "+
					"say who was given a copy of production, and sending that unencrypted to an "+
					"unauthenticated receiver is the thing the entries exist to prove is not "+
					"happening. Use syslog+tls://%s", cfg.Address, scheme, rest)
		default:
			return nil, fmt.Errorf("%s is not an address this sink understands; it is host:port", cfg.Address)
		}
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		// 6514 is the IANA registered port for syslog over TLS, so an address
		// with no port is completed rather than refused.
		address = net.JoinHostPort(address, "6514")
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("%s is not host:port: %w", cfg.Address, err)
	}

	conf := &tls.Config{
		ServerName: host,
		// 1.2 rather than the package default, because this carries an audit
		// stream and a receiver old enough to need 1.0 is a receiver whose
		// operator should be told rather than accommodated silently.
		MinVersion: tls.VersionTLS12,
	}
	if cfg.CAFile != "" {
		pem, readErr := os.ReadFile(cfg.CAFile)
		if readErr != nil {
			return nil, fmt.Errorf("the syslog CA file %s cannot be read: %w", cfg.CAFile, readErr)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf(
				"the syslog CA file %s holds no certificate this build could parse. "+
					"It is a PEM bundle, and a DER file or a private key is the usual mistake", cfg.CAFile)
		}
		conf.RootCAs = pool
	}
	switch {
	case cfg.CertFile != "" && cfg.KeyFile != "":
		pair, certErr := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if certErr != nil {
			return nil, fmt.Errorf("the syslog client certificate cannot be used: %w", certErr)
		}
		conf.Certificates = []tls.Certificate{pair}
	case cfg.CertFile != "" || cfg.KeyFile != "":
		// Refused rather than ignored. A receiver configured to require a
		// client certificate refuses a connection without one, and the error it
		// gives is a TLS handshake failure, which reads as a CA problem and
		// sends whoever is debugging it to the wrong file.
		return nil, fmt.Errorf(
			"a syslog client certificate needs both the certificate and the key, and only one is set")
	}

	cfg.Address = address
	if cfg.Hostname == "" {
		name, hostErr := os.Hostname()
		if hostErr != nil || name == "" {
			// NILVALUE. RFC 5424 says a field that is not known is "-", and a
			// receiver reading that knows it was not told, where "localhost"
			// would be a claim.
			name = "-"
		}
		cfg.Hostname = name
	}
	if cfg.AppName == "" {
		cfg.AppName = "antifailure"
	}
	return &Syslog{cfg: cfg, tls: conf, now: clockOf(cfg.Now)}, nil
}

// Name identifies the sink in the engine's own report of a forwarding failure.
func (s *Syslog) Name() string { return "syslog over TLS at " + s.cfg.Address }

// Write forwards one entry.
//
// The retry is one attempt on a fresh connection and no more, and the case it
// exists for is the ordinary one rather than an exotic failure: this holds a
// connection open between entries, a receiver or anything in front of it closes
// idle connections, and the first write after that closure fails on a socket
// that was fine when it was opened. Retrying that once is the difference
// between forwarding everything and forwarding everything except the entry
// after each idle period. A second retry would be a receiver that is actually
// down, and waiting through two more dial timeouts on a teardown is a cost with
// nothing to buy.
func (s *Syslog) Write(ctx context.Context, entry extension.AuditEntry) error {
	if !permitted(ctx) {
		return nil
	}
	body, err := encode(entry, s.now())
	if err != nil {
		return err
	}
	frame := s.frame(entry, body)

	reused, err := s.send(ctx, frame)
	switch {
	case err == nil:
		return nil
	case !reused:
		// The dial itself failed, so there is no stale connection to blame and
		// nothing a second attempt would do differently except spend another
		// dial timeout inside somebody's teardown.
		return fmt.Errorf("forwarding to %s: %w", s.Name(), err)
	case ctx.Err() != nil:
		// The caller went away. Not a receiver problem.
		return fmt.Errorf("forwarding to %s: %w", s.Name(), err)
	}

	s.drop()
	if _, err := s.send(ctx, frame); err != nil {
		return fmt.Errorf("forwarding to %s: %w", s.Name(), err)
	}
	return nil
}

// frame renders one RFC 5424 message with RFC 5425 octet counting.
//
// The MSG is the same JSON line every sink here writes, so an entry read out of
// a bucket and an entry read out of a SIEM are the same bytes and a query
// written against one works against the other.
func (s *Syslog) frame(entry extension.AuditEntry, body []byte) []byte {
	// The timestamp on the wire is when the action happened when the producer
	// said so, because a receiver orders and retains by this field. Falling
	// back to now is correct here and not a guessed timestamp: the JSON payload
	// still says occurred_at is absent, so the record does not claim to know
	// something it does not, while the framing header still gives the receiver
	// the monotonic value its retention needs.
	stamp := entry.OccurredAt
	if stamp.IsZero() {
		stamp = s.now()
	}

	var b strings.Builder
	fmt.Fprintf(&b, "<%d>%d %s %s %s %d %s - ",
		syslogFacility*8+syslogSeverity,
		syslogVersion,
		stamp.UTC().Format(time.RFC3339Nano),
		printable(s.cfg.Hostname, 255),
		printable(s.cfg.AppName, 48),
		os.Getpid(),
		printable(entry.Action, 32),
	)
	// The byte order mark, which RFC 5424 section 6.4 uses to declare that the
	// MSG is UTF-8. Without it a receiver is entitled to treat the payload as
	// an unknown encoding, and the ones that do render a JSON document with a
	// masked column name in it as mojibake.
	b.WriteString("\ufeff")
	b.Write(body)

	msg := b.String()
	return []byte(fmt.Sprintf("%d %s", len(msg), msg))
}

// send writes one frame, opening a connection if there is not one.
//
// reused says the write went onto a connection this sink already held, which is
// the only case a retry can help: a fresh connection that failed to write has
// nothing stale about it, and a dial that failed has no connection at all.
func (s *Syslog) send(ctx context.Context, frame []byte) (reused bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	reused = s.conn != nil
	if s.conn == nil {
		conn, dialErr := s.connect(ctx)
		if dialErr != nil {
			return false, dialErr
		}
		s.conn = conn
	}

	deadline := time.Now().Add(syslogWriteTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := s.conn.SetWriteDeadline(deadline); err != nil {
		return reused, err
	}
	if _, err := s.conn.Write(frame); err != nil {
		return reused, err
	}
	return reused, nil
}

// connect opens the TLS connection.
func (s *Syslog) connect(ctx context.Context) (net.Conn, error) {
	if s.cfg.dial != nil {
		return s.cfg.dial(ctx)
	}
	dialCtx, cancel := context.WithTimeout(ctx, syslogDialTimeout)
	defer cancel()
	dialer := &tls.Dialer{NetDialer: &net.Dialer{}, Config: s.tls}
	return dialer.DialContext(dialCtx, "tcp", s.cfg.Address)
}

// drop closes and forgets the connection so the next send opens a new one.
func (s *Syslog) drop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
	}
}

// Close releases the connection.
//
// Not part of extension.AuditSink, which is Name and Write and nothing else,
// and that is the right interface: the engine is a command that runs and exits,
// so the socket is closed by the operating system on the way out and a Close in
// the interface would be a method every implementer has to write and nothing
// has to call. It is here for the tests, which run many sinks in one process
// and would otherwise leak a connection per case.
func (s *Syslog) Close() error {
	s.drop()
	return nil
}

// printable trims a value to what RFC 5424 permits in a header field.
//
// Printable US-ASCII, no spaces, bounded length, and "-" for nothing. A header
// field carrying a space is a field that shifts every field after it, so a
// hostname with a space in it would move the audit payload into the message id
// and a receiver would file the entry under the wrong everything.
func printable(v string, max int) string {
	var b strings.Builder
	for _, r := range v {
		if r < 33 || r > 126 {
			continue
		}
		b.WriteRune(r)
		if b.Len() >= max {
			break
		}
	}
	if b.Len() == 0 {
		return "-"
	}
	return b.String()
}
