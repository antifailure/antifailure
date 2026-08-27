package personas

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

// The vectors are RFC 6238 Appendix B, the SHA-1 rows.
//
// Checked against the RFC rather than against this implementation's own
// output, which is the only way this test means anything: a test that asserts
// what the code already does proves the code has not changed, not that it is
// right. The RFC prints eight digit codes and this produces six, so the
// expectation is the last six, which is what truncation to six digits gives.
func TestTOTPMatchesRFC6238Vectors(t *testing.T) {
	// "12345678901234567890", the RFC's HMAC-SHA1 seed.
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).
		EncodeToString([]byte("12345678901234567890"))

	cases := []struct {
		unix  int64
		eight string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	}
	for _, c := range cases {
		got, err := TOTPCode(secret, time.Unix(c.unix, 0).UTC())
		if err != nil {
			t.Fatalf("T=%d: %v", c.unix, err)
		}
		want := c.eight[len(c.eight)-TOTPDigits:]
		if got != want {
			t.Errorf("T=%d: got %s, want %s (RFC prints %s)", c.unix, got, want, c.eight)
		}
	}
}

func TestTOTPValidAcceptsTheNeighbouringWindows(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).
		EncodeToString([]byte("12345678901234567890"))
	at := time.Unix(1111111111, 0).UTC()

	code, err := TOTPCode(secret, at)
	if err != nil {
		t.Fatal(err)
	}
	if !TOTPValid(secret, code, at) {
		t.Error("the code for this moment was refused for this moment")
	}
	// A code typed at the end of a window arrives in the next one, and a
	// server that refuses it produces a failure nobody can reproduce.
	if !TOTPValid(secret, code, at.Add(TOTPPeriod)) {
		t.Error("a code from the previous window was refused")
	}
	if TOTPValid(secret, code, at.Add(10*TOTPPeriod)) {
		t.Error("a code from ten windows ago was accepted, so the window is not bounded")
	}
}

func TestTOTPSecretAcceptsTheFormsItIsWrittenIn(t *testing.T) {
	raw := base32.StdEncoding.WithPadding(base32.NoPadding).
		EncodeToString([]byte("12345678901234567890"))
	at := time.Unix(1234567890, 0).UTC()
	want, err := TOTPCode(raw, at)
	if err != nil {
		t.Fatal(err)
	}

	// The spaced form an authenticator app displays, the padded form some
	// systems store, and lower case, which people paste.
	for _, form := range []string{
		strings.Join([]string{raw[:4], raw[4:8], raw[8:]}, " "),
		raw + "======",
		strings.ToLower(raw),
	} {
		got, err := TOTPCode(form, at)
		if err != nil {
			t.Fatalf("%q: %v", form, err)
		}
		if got != want {
			t.Errorf("%q gave %s, want %s", form, got, want)
		}
	}
}

func TestTOTPSecretRejectsWhatIsNotBase32(t *testing.T) {
	// A silently wrong key produces codes that are wrong forever and look
	// exactly like an application refusing a correct one.
	if _, err := TOTPCode("not base32 at all!", time.Now()); err == nil {
		t.Error("a secret that is not base32 was accepted")
	}
	if _, err := TOTPCode("", time.Now()); err == nil {
		t.Error("an empty secret was accepted")
	}
}
