// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package auditsink

// The syslog sink, against a collector that actually reads the frames.
//
// The collector below de-frames with the octet counting rule rather than
// looking for the JSON, which is the only way a framing defect is visible: a
// test that searched the byte stream for "golden.published" would pass for a
// message whose declared length was wrong, and a wrong length is precisely how
// one entry becomes four corrupt ones at a real receiver.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// collector is a syslog receiver over an in-memory pipe.
//
// net.Pipe rather than a TLS listener with a generated certificate authority,
// because what is under test is the framing and the retry, and a real
// handshake would make every case here depend on a certificate fixture that
// expires. The TLS refusal is tested separately, at construction, which is
// where it is decided.
type collector struct {
	mu       sync.Mutex
	messages []string
	dials    int
	// peers are the receiving ends of every connection handed out, so a test
	// can close one and leave the sink holding a socket whose far side is
	// gone. That is what an idle timeout at the receiver looks like from here,
	// and it is the only state in which the retry can be observed at all: a
	// sink that dialled fresh and failed has nothing stale to blame.
	peers []net.Conn
	// refuseDial makes the dial itself fail, which is a receiver that is down.
	refuseDial bool
	// onMessage is called the instant a message has been de-framed, which is
	// the only point at which a receiver could really have parsed the entry.
	// The benchmark stops its clock here rather than when Write returns,
	// because a write that returns with bytes still in a kernel buffer has not
	// put anything in anybody's SIEM.
	onMessage func()
}

func recordingSyslog(t *testing.T) *collector {
	t.Helper()
	return &collector{}
}

func (c *collector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.messages)
}

func (c *collector) taken() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.messages...)
}

func (c *collector) dialCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dials
}

// dial hands the sink one end of a pipe and reads frames off the other.
func (c *collector) dial(context.Context) (net.Conn, error) {
	c.mu.Lock()
	c.dials++
	refuse := c.refuseDial
	c.mu.Unlock()

	if refuse {
		return nil, errors.New("dial tcp 10.0.0.9:6514: connect: connection refused")
	}

	mine, theirs := net.Pipe()
	c.mu.Lock()
	c.peers = append(c.peers, theirs)
	c.mu.Unlock()
	go c.read(theirs)
	return mine, nil
}

// hangUp closes the receiving end the sink is currently holding, without the
// sink being told, which is what something in front of a SIEM does to an idle
// connection.
func (c *collector) hangUp(t *testing.T) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	require.NotEmpty(t, c.peers, "there is no connection to close")
	_ = c.peers[len(c.peers)-1].Close()
}

// read de-frames with the RFC 5425 octet counting rule.
func (c *collector) read(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	for {
		digits, err := r.ReadString(' ')
		if err != nil {
			return
		}
		length, err := strconv.Atoi(strings.TrimSpace(digits))
		if err != nil || length <= 0 {
			return
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(r, body); err != nil {
			return
		}
		c.mu.Lock()
		c.messages = append(c.messages, string(body))
		notify := c.onMessage
		c.mu.Unlock()
		if notify != nil {
			notify()
		}
	}
}

// newCollectedSyslog builds a sink writing into a collector.
func newCollectedSyslog(t *testing.T, c *collector) *Syslog {
	t.Helper()
	s, err := NewSyslog(SyslogConfig{
		Address:  "siem.acme.example:6514",
		Hostname: "runner-7",
		Now:      at(forwarded),
		dial:     c.dial,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// ---------------------------------------------------------------------------
// The message on the wire
// ---------------------------------------------------------------------------

func TestSyslogFramesOneMessageThatDeFramesByLength(t *testing.T) {
	t.Parallel()
	c := recordingSyslog(t)
	s := newCollectedSyslog(t, c)

	// A newline in the detail, which is the case the framing choice was made
	// for: a policy refusal written for a terminal has newlines in it, and the
	// trailing-newline framing in RFC 6587 cannot carry one.
	e := entry()
	e.Detail = map[string]any{"refusal": "organization policy requires:\n  api.stripe.com blocked"}
	require.NoError(t, s.Write(licensed(t), e))

	require.Eventually(t, func() bool { return c.count() == 1 }, 5*time.Second, 5*time.Millisecond,
		"the collector never de-framed a message, so the declared length is wrong")

	msg := c.taken()[0]
	require.Contains(t, msg, "refusal",
		"a message with a newline in it did not survive framing")
	require.Contains(t, msg, "api.stripe.com blocked")
}

func TestSyslogUsesTheAuditFacilityAndTheActionAsTheMessageID(t *testing.T) {
	t.Parallel()
	// Facility 13 is "log audit" from RFC 5424 table 1, severity 6 is
	// informational, so the priority is 13*8+6. A receiver routes on facility,
	// and an audit stream arriving as facility 1 lands in the same bucket as
	// everything else a machine says.
	c := recordingSyslog(t)
	s := newCollectedSyslog(t, c)
	require.NoError(t, s.Write(licensed(t), entry()))
	require.Eventually(t, func() bool { return c.count() == 1 }, 5*time.Second, 5*time.Millisecond)

	msg := c.taken()[0]
	require.True(t, strings.HasPrefix(msg, "<110>1 "),
		"the priority is not audit/informational: %s", firstLine(msg))

	fields := strings.SplitN(msg, " ", 7)
	require.Len(t, fields, 7)
	require.Equal(t, "2026-09-07T11:22:33.456789Z", fields[1],
		"the header timestamp is when the action happened, which is what a receiver retains by")
	require.Equal(t, "runner-7", fields[2])
	require.Equal(t, "antifailure", fields[3])
	require.Equal(t, strconv.Itoa(os.Getpid()), fields[4])
	require.Equal(t, "golden.published", fields[5],
		"the action is the message id, which is what a receiver filters on")
}

func TestSyslogDeclaresItsPayloadAsUTF8(t *testing.T) {
	t.Parallel()
	// RFC 5424 section 6.4: without the byte order mark a receiver is entitled
	// to treat the MSG as an unknown encoding, and the ones that do render a
	// masked column name as mojibake.
	c := recordingSyslog(t)
	s := newCollectedSyslog(t, c)
	require.NoError(t, s.Write(licensed(t), entry()))
	require.Eventually(t, func() bool { return c.count() == 1 }, 5*time.Second, 5*time.Millisecond)

	msg := c.taken()[0]
	mark := strings.Index(msg, "\ufeff")
	require.GreaterOrEqual(t, mark, 0, "the payload is not declared as UTF-8")
	require.True(t, strings.HasPrefix(msg[mark+len("\ufeff"):], "{"),
		"the JSON does not start immediately after the byte order mark")
}

func TestSyslogHeaderFieldsCannotShiftTheFieldsAfterThem(t *testing.T) {
	t.Parallel()
	// A header field carrying a space shifts every field after it, so a
	// hostname with a space in it would move the audit payload into the
	// message id and a receiver would file the entry under the wrong
	// everything.
	s, err := NewSyslog(SyslogConfig{
		Address:  "siem.acme.example:6514",
		Hostname: "build box 7",
		Now:      at(forwarded),
	})
	require.NoError(t, err)
	framed := string(s.frame(entry(), []byte("{}")))

	_, msg, found := strings.Cut(framed, " ")
	require.True(t, found)
	fields := strings.SplitN(msg, " ", 7)
	require.Len(t, fields, 7)
	require.Equal(t, "buildbox7", fields[2], "a space in a header field shifted the message")
	require.Equal(t, "golden.published", fields[5])
}

func TestSyslogSaysNothingRatherThanClaimingAHostnameItDoesNotHave(t *testing.T) {
	t.Parallel()
	// NILVALUE. RFC 5424 says a field that is not known is "-", and a receiver
	// reading that knows it was not told, where "localhost" would be a claim.
	s, err := NewSyslog(SyslogConfig{
		Address: "siem.acme.example:6514", Hostname: "\x00\x01", Now: at(forwarded),
	})
	require.NoError(t, err)
	framed := string(s.frame(entry(), []byte("{}")))
	fields := strings.SplitN(framed, " ", 8)
	require.Equal(t, "-", fields[3], "an unusable hostname became a claim rather than a nil value")
}

// ---------------------------------------------------------------------------
// Reaching the receiver
// ---------------------------------------------------------------------------

func TestSyslogRetriesOnceOnAConnectionThatWentStaleWhileIdle(t *testing.T) {
	t.Parallel()
	// The ordinary case rather than an exotic one: this holds a connection open
	// between entries, a receiver or something in front of it closes idle
	// connections, and the first write after that closure fails on a socket
	// that was fine when it was opened. Without the retry an installation
	// forwards everything except the entry after each idle period.
	c := recordingSyslog(t)
	s := newCollectedSyslog(t, c)

	// One good entry, so the sink is holding a connection.
	require.NoError(t, s.Write(licensed(t), entry()))
	require.Eventually(t, func() bool { return c.count() == 1 }, 5*time.Second, 5*time.Millisecond)

	// Now break the one it holds, the way an idle timeout does, without
	// telling the sink. It still believes it has a connection, which is the
	// state the retry exists for.
	c.hangUp(t)
	require.Equal(t, 1, c.dialCount())

	second := entry()
	second.Action = "environment.torn_down"
	require.NoError(t, s.Write(licensed(t), second),
		"an entry after an idle period was lost rather than retried")
	require.Eventually(t, func() bool { return c.count() == 2 }, 5*time.Second, 5*time.Millisecond)
	require.Contains(t, c.taken()[1], "environment.torn_down")
	require.Equal(t, 2, c.dialCount(),
		"the retry reused the connection it had just been refused on")
}

func TestSyslogDoesNotSpendASecondDialOnAReceiverThatIsDown(t *testing.T) {
	t.Parallel()
	// A dial that failed has no stale connection to blame, and a second
	// attempt would spend another dial timeout inside somebody's teardown for
	// nothing.
	c := recordingSyslog(t)
	c.refuseDial = true
	s := newCollectedSyslog(t, c)

	err := s.Write(licensed(t), entry())
	require.Error(t, err)
	require.ErrorContains(t, err, "siem.acme.example:6514",
		"the failure did not name the destination the operator has to go and fix")
	require.Equal(t, 1, c.dialCount(), "a dead receiver was dialled more than once")
}

// ---------------------------------------------------------------------------
// There is no plaintext option
// ---------------------------------------------------------------------------

func TestSyslogRefusesPlaintextRatherThanDowngradingToIt(t *testing.T) {
	t.Parallel()
	// The entries say who was given a copy of production. Putting that on the
	// network in the clear, to an unauthenticated receiver, is precisely the
	// property the entries exist to prove is not happening.
	for _, scheme := range []string{"syslog", "tcp", "udp"} {
		t.Run(scheme, func(t *testing.T) {
			_, err := NewSyslog(SyslogConfig{Address: scheme + "://collector.acme.example:514"})
			require.Error(t, err, "%s:// was accepted and would send the audit stream in the clear", scheme)
			require.ErrorContains(t, err, "TLS only")
			require.ErrorContains(t, err, "syslog+tls://collector.acme.example:514",
				"the refusal did not say what to write instead")
		})
	}
}

func TestSyslogAcceptsTheTLSSpellingsAndCompletesTheRegisteredPort(t *testing.T) {
	t.Parallel()
	// 6514 is the IANA registered port for syslog over TLS, so an address with
	// no port is completed rather than refused.
	for _, address := range []string{
		"syslog+tls://siem.acme.example", "syslogs://siem.acme.example",
		"tls://siem.acme.example", "siem.acme.example",
	} {
		t.Run(address, func(t *testing.T) {
			s, err := NewSyslog(SyslogConfig{Address: address})
			require.NoError(t, err)
			require.Equal(t, "syslog over TLS at siem.acme.example:6514", s.Name())
		})
	}
}

func TestSyslogRefusesHalfOfAClientCertificate(t *testing.T) {
	t.Parallel()
	// Ignored instead of refused, this surfaces as a TLS handshake failure at
	// the receiver, which reads as a CA problem and sends whoever is debugging
	// it to the wrong file.
	_, err := NewSyslog(SyslogConfig{
		Address: "siem.acme.example:6514", CertFile: "/tmp/client.pem",
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "both the certificate and the key")
}

func TestSyslogSaysWhichFileIsWrongWhenTheCABundleIsNot(t *testing.T) {
	t.Parallel()
	// A DER file or a private key where a PEM bundle belongs is the usual
	// mistake, and it has to be caught when the binary starts rather than
	// twenty minutes later inside a teardown nobody is watching.
	path := filepath.Join(t.TempDir(), "ca.der")
	require.NoError(t, os.WriteFile(path, []byte{0x30, 0x82, 0x01}, 0o600))

	_, err := NewSyslog(SyslogConfig{Address: "siem.acme.example:6514", CAFile: path})
	require.Error(t, err)
	require.ErrorContains(t, err, path)
	require.ErrorContains(t, err, "PEM bundle")

	_, err = NewSyslog(SyslogConfig{
		Address: "siem.acme.example:6514", CAFile: filepath.Join(t.TempDir(), "absent.pem"),
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "cannot be read")
}

func TestSyslogWithNoAddressIsRefusedRatherThanBuiltAndSilent(t *testing.T) {
	t.Parallel()
	_, err := NewSyslog(SyslogConfig{})
	require.Error(t, err)
	require.ErrorContains(t, err, "host:port")
}

// firstLine keeps a failure message readable when the payload is long.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if len(s) > 200 {
		return fmt.Sprintf("%s...", s[:200])
	}
	return s
}
