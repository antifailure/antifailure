// Package verify reads a masked database back and looks for anything that
// still parses as personal data.
//
// This package is the difference between "we masked it" and "we checked".
// Masking is a set of rules someone wrote, and rules have gaps: a column
// nobody classified, a JSON document with an email nested three levels down, a
// value copied into an audit table by a trigger. The scanner does not trust
// the rules. It reads every column of every table and asks, of each value,
// whether it still looks like something that identifies a person.
//
// Two rules govern the output, and both matter more than they look.
//
// A finding never contains the value. It names the table, the column, and the
// detector, and stops. Printing the value would move the leak from the
// database into the log, the event stream, the pull request comment, and the
// terminal history of whoever ran the command.
//
// A golden that fails cannot be branched. That is enforced in the provider
// interface rather than documented, so there is no path where an unverified
// copy reaches an environment.
package verify

import (
	"regexp"
	"strings"
	"unicode"
)

// Detector recognises one kind of personal data.
type Detector struct {
	// Name identifies the detector in findings and in the attestation.
	Name string
	// Describe is one sentence for the attestation and the documentation.
	Describe string
	// Match reports whether a value looks like this kind of data.
	Match func(string) bool
	// Severity ranks findings so that a report leads with the worst.
	Severity int
}

// Severity levels. A card number is worse than a name, and a report that
// treats them alike buries the thing that matters.
const (
	SeverityLow    = 1
	SeverityMedium = 2
	SeverityHigh   = 3
)

// Detectors returns the built in detector set, ordered by severity.
//
// The set is deliberately narrow on shapes that produce false positives at
// scale. A detector that fires on every UUID or every long hex string makes an
// operator turn verification off, and a verification nobody runs protects
// nothing. The cost of that narrowness is covered by the source value check,
// which catches whatever the shape detectors miss by looking for the actual
// values that were in the source.
func Detectors() []Detector {
	return []Detector{
		{
			Name:     "private-key",
			Describe: "A PEM encoded private key.",
			Severity: SeverityHigh,
			Match: func(s string) bool {
				return strings.Contains(s, "-----BEGIN") && strings.Contains(s, "PRIVATE KEY")
			},
		},
		{
			Name:     "credential",
			Describe: "A credential carrying a known provider prefix.",
			Severity: SeverityHigh,
			Match:    matchCredential,
		},
		{
			Name:     "payment-card",
			Describe: "A sequence of 13 to 19 digits that passes the Luhn check and starts with a real issuer prefix.",
			Severity: SeverityHigh,
			Match:    matchPaymentCard,
		},
		{
			Name:     "national-id",
			Describe: "A United States social security number in its written form.",
			Severity: SeverityHigh,
			Match:    matchSSN,
		},
		{
			Name:     "iban",
			Describe: "An international bank account number that passes its checksum.",
			Severity: SeverityHigh,
			Match:    matchIBAN,
		},
		{
			Name:     "jwt",
			Describe: "A JSON Web Token, which usually carries an identity claim.",
			Severity: SeverityHigh,
			Match:    jwtRe.MatchString,
		},
		{
			Name:     "email",
			Describe: "An email address outside the reserved domains masking produces.",
			Severity: SeverityMedium,
			Match:    matchEmail,
		},
		{
			Name:     "phone",
			Describe: "A phone number in E.164 or a common national format.",
			Severity: SeverityMedium,
			Match:    matchPhone,
		},
		{
			Name:     "ip-address",
			Describe: "A routable IP address, outside the documentation ranges masking produces.",
			Severity: SeverityLow,
			Match:    matchRoutableIP,
		},
	}
}

var (
	// ssnRe matches the shape. The rules about which values are actually
	// issued are applied afterwards in Go, because RE2 has no lookahead: it
	// guarantees linear time matching, and lookahead is one of the things it
	// gives up to do that. Writing the rules out is clearer anyway.
	ssnRe = regexp.MustCompile(`\b(\d{3})-(\d{2})-(\d{4})\b`)
	jwtRe = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)
	// emailRe is deliberately stricter than RFC 5322. The looser the pattern,
	// the more version strings and file paths it matches, and every false
	// positive is an operator's afternoon.
	emailRe         = regexp.MustCompile(`\b[A-Za-z0-9][A-Za-z0-9._%+-]{0,63}@[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z]{2,24})+\b`)
	e164Re          = regexp.MustCompile(`\+[1-9]\d{7,14}\b`)
	nationalPhoneRe = regexp.MustCompile(`\b\(?0?[1-9]\d{2}\)?[ .-]\d{3}[ .-]\d{4}\b`)
	ipv4Re          = regexp.MustCompile(`\b(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})\b`)
	ibanRe          = regexp.MustCompile(`\b[A-Z]{2}\d{2}[A-Z0-9]{10,30}\b`)
)

// shape summarises what a value contains, in one pass.
//
// Verification reads every column of every table, so the cost per value
// decides whether a twenty gigabyte scan finishes in minutes or hours. Almost
// every value fails almost every detector, and a regexp scan to establish that
// is roughly a hundred times more expensive than looking for the one byte the
// pattern cannot match without. One pass over the bytes answers all nine
// questions cheaply, and only the detectors that survive it run their pattern.
type shape struct {
	hasAt      bool
	hasDigit   bool
	hasPlus    bool
	hasDash    bool
	hasDot     bool
	hasUpper   bool
	digitCount int
}

func shapeOf(s string) shape {
	var sh shape
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '@':
			sh.hasAt = true
		case c >= '0' && c <= '9':
			sh.hasDigit = true
			sh.digitCount++
		case c == '+':
			sh.hasPlus = true
		case c == '-':
			sh.hasDash = true
		case c == '.':
			sh.hasDot = true
		case c >= 'A' && c <= 'Z':
			sh.hasUpper = true
		}
	}
	return sh
}

// syntheticDomains are the domains masking produces and the ones reserved for
// documentation and testing. A value in one of them is by construction not a
// real address, so matching it would guarantee a finding on every masked row.
var syntheticDomains = []string{
	".test", ".invalid", ".example", ".localhost",
	"example.com", "example.org", "example.net",
}

func matchEmail(s string) bool {
	sh := shapeOf(s)
	if !sh.hasAt || !sh.hasDot {
		return false
	}
	for _, m := range emailRe.FindAllString(s, 8) {
		lower := strings.ToLower(m)
		synthetic := false
		for _, d := range syntheticDomains {
			if strings.HasSuffix(lower, d) {
				synthetic = true
				break
			}
		}
		if !synthetic {
			return true
		}
	}
	return false
}

// matchSSN applies the ranges the Social Security Administration never issues.
//
// Without them the pattern matches a date range, a part number, and a phone
// number written with dashes, which a masked database contains in quantity.
func matchSSN(s string) bool {
	if sh := shapeOf(s); !sh.hasDash || sh.digitCount < 9 {
		return false
	}
	for _, m := range ssnRe.FindAllStringSubmatch(s, 8) {
		area, group, serial := m[1], m[2], m[3]
		switch {
		case area == "000" || area == "666" || area[0] == '9':
			continue // never issued
		case group == "00":
			continue // never issued
		case serial == "0000":
			continue // never issued
		}
		return true
	}
	return false
}

func matchPhone(s string) bool {
	sh := shapeOf(s)
	if sh.digitCount < 8 {
		return false
	}
	if sh.hasPlus && e164Re.MatchString(s) {
		return true
	}
	// A national format has to be corroborated, because the same shape matches
	// a date range, a part number, and a version string. Requiring a nearby
	// word that means "phone" cuts the false positive rate to near zero at the
	// cost of missing a bare column of numbers, which the column name
	// heuristic in the scanner catches instead.
	lower := strings.ToLower(s)
	corroborated := false
	for _, hint := range []string{"phone", "tel", "mobile", "cell", "fax", "contact"} {
		if strings.Contains(lower, hint) {
			corroborated = true
			break
		}
	}
	if !corroborated {
		return false
	}
	return nationalPhoneRe.MatchString(s)
}

// cardPrefixes are the issuer identification ranges that actually exist.
// Requiring one turns "any Luhn valid digit run" into "a number that could be
// a card", which matters because Luhn passes on roughly one in ten random
// digit sequences.
var cardPrefixes = []string{
	"4",                          // Visa
	"51", "52", "53", "54", "55", // Mastercard
	"34", "37", // American Express
	"6011", "644", "645", "646", "647", "648", "649", "65", // Discover
	"36", "38", "300", "301", "302", "303", "304", "305", // Diners
	"3528", "3529", "353", "354", "355", "356", "357", "358", // JCB
}

// maskedCardPrefix is what the credit_card transform produces. A masked value
// must never be reported as a finding, or every masked card column fails.
const maskedCardPrefix = "4242"

func matchPaymentCard(s string) bool {
	if shapeOf(s).digitCount < 13 {
		return false
	}
	for _, run := range digitRuns(s, 13, 19) {
		if strings.HasPrefix(run, maskedCardPrefix) {
			continue
		}
		hasPrefix := false
		for _, p := range cardPrefixes {
			if strings.HasPrefix(run, p) {
				hasPrefix = true
				break
			}
		}
		if hasPrefix && luhnValid(run) {
			return true
		}
	}
	return false
}

// digitRuns extracts maximal digit sequences, ignoring the spaces and dashes a
// card number is usually written with.
// digitRuns finds the runs of digits that could be a number somebody wrote.
//
// A run glued to a letter is not one of them, and that exclusion is the whole
// reason this function is more than three lines. A sha256 digest is sixty-four
// hex characters, so it contains long runs of digits separated by the letters
// a to f, and with a thousand digests one of those runs will eventually be
// thirteen digits, start with a real issuer prefix, and pass the Luhn check by
// chance. The scanner then refuses to publish a golden because a checksum
// column looks like a payment card, which is a finding nobody can act on and
// the fastest way to teach somebody to turn verification off.
//
// What is given up is a card number written with a letter immediately against
// it, with no space, punctuation, or quote between them. That is not how
// anybody writes a card number, and the scan for values known to be in the
// source catches it anyway.
func digitRuns(s string, minLen, maxLen int) []string {
	rs := []rune(s)
	var out []string
	var cur strings.Builder
	// Where the current run starts and ends in rs, so its neighbours can be
	// looked at. A run's own contents cannot say whether it is a token or a
	// number; only what is on either side of it can.
	first, last := -1, -1

	flush := func() {
		if first >= 0 && cur.Len() >= minLen && cur.Len() <= maxLen {
			gluedBefore := first > 0 && unicode.IsLetter(rs[first-1])
			gluedAfter := last+1 < len(rs) && unicode.IsLetter(rs[last+1])
			if !gluedBefore && !gluedAfter {
				out = append(out, cur.String())
			}
		}
		cur.Reset()
		first, last = -1, -1
	}

	for i, r := range rs {
		switch {
		case unicode.IsDigit(r):
			if first < 0 {
				first = i
			}
			last = i
			cur.WriteRune(r)
		case r == ' ' || r == '-':
			// A separator inside a number is skipped rather than ending the
			// run, which is how "4111 1111 1111 1111" is seen as one number.
			// It does not move `last`, so a card followed by a space and then
			// a word is still a card rather than something glued to a letter.
		default:
			flush()
		}
	}
	flush()
	return out
}

func luhnValid(digits string) bool {
	sum, double := 0, false
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		double = !double
		sum += d
	}
	return sum%10 == 0
}

func matchIBAN(s string) bool {
	sh := shapeOf(s)
	if !sh.hasUpper || !sh.hasDigit || len(s) < 15 {
		return false
	}
	for _, m := range ibanRe.FindAllString(s, 4) {
		if ibanChecksumValid(m) {
			return true
		}
	}
	return false
}

// ibanChecksumValid runs the mod 97 check. Without it the pattern matches
// every uppercase alphanumeric run, which appears in every base32 identifier a
// masked database is full of.
func ibanChecksumValid(iban string) bool {
	if len(iban) < 15 || len(iban) > 34 {
		return false
	}
	rearranged := iban[4:] + iban[:4]
	remainder := 0
	for _, r := range rearranged {
		var v int
		switch {
		case r >= '0' && r <= '9':
			v = int(r - '0')
			remainder = (remainder*10 + v) % 97
		case r >= 'A' && r <= 'Z':
			v = int(r-'A') + 10
			remainder = (remainder*100 + v) % 97
		default:
			return false
		}
	}
	return remainder == 1
}

// credentialPrefixes are provider key formats. Split so that this source file
// does not itself contain a string a secret scanner matches.
var credentialPrefixes = []string{
	"sk" + "_live_", "sk" + "_test_", "rk" + "_live_", "wh" + "sec_",
	"gh" + "p_", "gh" + "o_", "gh" + "s_", "gh" + "u_", "gh" + "r_",
	"github" + "_pat_", "AK" + "IA", "AS" + "IA", "xo" + "xb-", "xo" + "xp-",
	"S" + "G.", "sk" + "-ant-", "sb" + "p_", "na" + "pi_", "np" + "m_",
	"AI" + "za", "dp" + ".st.", "dp" + ".pt.",
}

func matchCredential(s string) bool {
	for _, p := range credentialPrefixes {
		if i := strings.Index(s, p); i >= 0 {
			// A prefix alone is a word. A prefix followed by enough entropy is
			// a credential.
			if len(s)-i >= len(p)+16 {
				return true
			}
		}
	}
	return false
}

// documentationRanges are what the ip transform produces, plus the private and
// loopback ranges, which identify nobody.
func matchRoutableIP(s string) bool {
	sh := shapeOf(s)
	if !sh.hasDot || sh.digitCount < 4 {
		return false
	}
	for _, m := range ipv4Re.FindAllStringSubmatch(s, 8) {
		octets := [4]int{}
		valid := true
		for i := 1; i <= 4; i++ {
			n := 0
			for _, r := range m[i] {
				n = n*10 + int(r-'0')
			}
			if n > 255 || (len(m[i]) > 1 && m[i][0] == '0') {
				valid = false
				break
			}
			octets[i-1] = n
		}
		if !valid {
			continue
		}
		if isReservedIP(octets) {
			continue
		}
		return true
	}
	return false
}

func isReservedIP(o [4]int) bool {
	switch {
	case o[0] == 10: // private
		return true
	case o[0] == 127: // loopback
		return true
	case o[0] == 172 && o[1] >= 16 && o[1] <= 31: // private
		return true
	case o[0] == 192 && o[1] == 168: // private
		return true
	case o[0] == 192 && o[1] == 0 && o[2] == 2: // RFC 5737 documentation
		return true
	case o[0] == 198 && (o[1] == 51 || o[1] == 18 || o[1] == 19): // documentation and benchmarking
		return true
	case o[0] == 203 && o[1] == 0 && o[2] == 113: // RFC 5737 documentation
		return true
	case o[0] == 169 && o[1] == 254: // link local
		return true
	case o[0] == 0 || o[0] >= 224: // this network, multicast, reserved
		return true
	}
	return false
}
