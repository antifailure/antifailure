package personas

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Time based one time passwords, RFC 6238 over RFC 4226.
//
// Implemented here rather than pulled in, for two reasons. It is forty lines
// of standard library, and this repository pins its supply chain deliberately,
// so a dependency for forty lines is a dependency to review, pin, scan and
// eventually update forever. The second reason matters more: both sides of
// this need it. The adapter enrols the secret and the runner produces the code
// from it, and having one implementation means they cannot disagree about
// digits, period or algorithm, which is the usual way a TOTP integration
// fails while every unit test passes.
//
// SHA-1 is the algorithm here, and that is correct rather than an oversight.
// RFC 6238 specifies HMAC-SHA1 as the default, every authenticator app
// implements it, and it is what Supabase, Auth0 and the common libraries
// enrol by default. HMAC-SHA1 is not affected by the collision attacks that
// make SHA-1 unsuitable for signatures.

// TOTPPeriod is the window length every common implementation uses.
const TOTPPeriod = 30 * time.Second

// TOTPDigits is the code length every common implementation uses.
const TOTPDigits = 6

// TOTPCode returns the code for a secret at a moment.
//
// The secret is base32 without padding, which is the form enrolment produces
// and the form an otpauth URL carries.
func TOTPCode(secret string, at time.Time) (string, error) {
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return "", err
	}
	counter := uint64(at.UTC().Unix()) / uint64(TOTPPeriod.Seconds())
	return hotp(key, counter), nil
}

// TOTPCodeAt returns the code for a counter, which is what a test that has to
// pin down a window uses.
func TOTPCodeAt(secret string, counter uint64) (string, error) {
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return "", err
	}
	return hotp(key, counter), nil
}

// TOTPValid reports whether a code is the one for a moment.
//
// A one window tolerance either side, which is what a server does, because a
// code typed at the end of a window arrives in the next one. Without it a
// test that runs at the wrong instant fails one time in thirty and gets called
// flaky rather than fixed.
func TOTPValid(secret, code string, at time.Time) bool {
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return false
	}
	counter := uint64(at.UTC().Unix()) / uint64(TOTPPeriod.Seconds())
	for _, c := range []uint64{counter - 1, counter, counter + 1} {
		if hmac.Equal([]byte(hotp(key, c)), []byte(code)) {
			return true
		}
	}
	return false
}

// hotp is RFC 4226: HMAC the counter, take the dynamic truncation the RFC
// specifies, and reduce it to the digit count.
func hotp(key []byte, counter uint64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)

	// The low four bits of the last byte choose where to read from, which is
	// what makes the truncation dynamic rather than a fixed prefix.
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff

	mod := uint32(1)
	for i := 0; i < TOTPDigits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", TOTPDigits, value%mod)
}

// decodeTOTPSecret accepts the secret in the forms it is written in.
//
// Authenticator apps show the secret in spaced groups and some systems store
// it padded, so both are accepted rather than rejected as malformed. Anything
// that is not base32 at all is an error, because a silently wrong key produces
// codes that are wrong forever and look like a broken application.
func decodeTOTPSecret(secret string) ([]byte, error) {
	cleaned := strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(secret))
	cleaned = strings.TrimRight(cleaned, "=")
	if cleaned == "" {
		return nil, fmt.Errorf("the TOTP secret is empty")
	}
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(cleaned)
	if err != nil {
		return nil, fmt.Errorf("the TOTP secret is not base32: %w", err)
	}
	return key, nil
}

// TOTPURI returns the otpauth URL for a secret.
//
// Written into the persona metadata so that a person debugging a run can add
// the same factor to their own authenticator and see what the agent sees.
// Every parameter is stated rather than left to a default, because the
// defaults are what differ between implementations.
func TOTPURI(issuer, account, secret string) string {
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprintf("%d", TOTPDigits))
	q.Set("period", fmt.Sprintf("%d", int(TOTPPeriod.Seconds())))
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + q.Encode()
}
