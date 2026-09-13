package secret

// Parsing an address that can carry a credential, without the credential
// reaching the error.
//
// net/url builds a parse error from the raw string, and (*url.Error).Error
// quotes the whole of it. A connection string with one wrong character in it
// therefore prints its own password, and net/http hands that error back
// unchanged from NewRequestWithContext. The client rewrites a password to ***
// when a request FAILS; an address that never parses never reaches that code.
//
// Quoting only the inner half of the error is not enough, because the inner
// half quotes pieces of the address too. An unescaped slash in a password ends
// the authority early, and "invalid port" then quotes the start of the
// password. A stray percent sign quotes the two bytes after it. pgx strips the
// outer half of a URL error and prints the inner half, so it leaks those pieces
// as well.
//
// So the address is redacted BEFORE anything describes it, and the description
// comes from parsing the redacted address. No error built here has seen the
// credential, which is a stronger property than an error that had the
// credential scrubbed out of it afterwards by a pattern that has to anticipate
// every way a message can quote one.

import (
	"errors"
	"net/url"
	"strings"
)

// urlMask stands in for a credential inside an address.
//
// Not Redacted. A square bracket is not a legal character in a URL's user
// information, so an address redacted to "[redacted]@host" would fail to parse
// for a reason of its own and the real fault would be reported as that one.
// Three stars are legal there, and are what net/http prints for a password.
const urlMask = "***"

// errUnquotable is the reason given when the fault is inside the part of the
// address that was removed before parsing it again.
var errUnquotable = errors.New("the part that does not parse is not quoted, because it is " +
	"where a credential goes: everything before the last @, a port that is not a number, " +
	"or the query. A password has to percent encode any character outside A to Z, 0 to 9 " +
	"and -._~")

// ParseURL is url.Parse for an address that can carry a credential.
//
// A parsed address is returned exactly as url.Parse returns it. The difference
// is the error: it is always a *url.Error produced by parsing RedactURL(raw),
// never raw itself, so it can be returned, wrapped, logged or sent to a service.
// When the fault survives the redaction it lies in a part that holds no
// credential, and the standard library's own words for it are kept, so the
// reader is still told what is wrong. When it does not survive, the error says
// which parts were withheld.
func ParseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err == nil {
		return u, nil
	}
	shown := RedactURL(raw)
	if _, again := url.Parse(shown); again != nil {
		return nil, again
	}
	return nil, &url.Error{Op: "parse", URL: shown, Err: errUnquotable}
}

// RedactURL returns an address with anything that can hold a credential
// replaced, for use in a message.
//
// It works on the text rather than on a parsed URL, because the address it is
// most needed for is one that does not parse. Three things go.
//
// Everything before the LAST @ after the scheme. The last, because that is
// where net/url splits user information from the host, and because an
// unescaped @ in a password is the commonest reason there are two. Cutting
// there also covers a password holding a slash, a question mark or a hash,
// each of which would otherwise end the authority early and leave the rest of
// the password looking like a host, a path or a fragment. The user name goes
// with the password, because a token is often sent as the user name alone.
//
// A port that is not a number, when there is no @ at all, together with
// everything after it. "postgres://admin:hunter2" with the host forgotten
// parses the password as a port, and whatever followed a slash in it as a path.
//
// The query and the fragment, which is where a signed URL, a shared access
// signature and a webhook token live.
//
// The scheme, the host and the path stay, because they are what a reader needs
// to find the setting that is wrong. The rule errs toward removing too much: an
// @ in a query takes the host and path with it. A message that names less than
// it could is the cheaper of the two failures.
func RedactURL(raw string) string {
	head, rest := splitScheme(raw)
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = urlMask + rest[at:]
	} else if head != "" {
		authority := rest
		if end := strings.IndexAny(rest, "/?#"); end >= 0 {
			authority = rest[:end]
		}
		if colon := portColon(authority); colon >= 0 && !allDigits(authority[colon+1:]) {
			return head + authority[:colon+1] + urlMask
		}
	}
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i+1] + urlMask
	}
	return head + rest
}

// splitScheme separates a leading "scheme://" or "//" from the rest.
//
// Only a real scheme counts. A double slash found anywhere else can be inside a
// password, and treating what precedes it as a scheme would keep it in plain
// sight.
func splitScheme(raw string) (head, rest string) {
	i := strings.Index(raw, "//")
	if i < 0 {
		return "", raw
	}
	if i == 0 {
		return raw[:2], raw[2:]
	}
	if raw[i-1] != ':' || !validScheme(raw[:i-1]) {
		return "", raw
	}
	return raw[:i+2], raw[i+2:]
}

// validScheme is RFC 3986's scheme production: a letter, then letters, digits,
// plus, minus and dot.
func validScheme(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z':
		case i > 0 && ('0' <= c && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return true
}

// portColon is the index of the colon that introduces a port in an authority
// with no user information, or -1. An IP literal's own colons are inside its
// brackets and do not count.
func portColon(authority string) int {
	if strings.HasPrefix(authority, "[") {
		end := strings.LastIndex(authority, "]")
		if end < 0 {
			return -1
		}
		if i := strings.IndexByte(authority[end:], ':'); i >= 0 {
			return end + i
		}
		return -1
	}
	return strings.LastIndex(authority, ":")
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
