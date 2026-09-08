package schema

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrTooFine is a CPU share smaller than a thousandth of a core.
//
// A sentinel rather than a string, because the caller has to tell it apart
// from "this is not a quantity" and the two say different things to whoever
// wrote the line. 0.0001 IS a quantity; what is wrong with it is that a
// thousandth is the finest share either runtime can hold, so it would round to
// no cap at all, which is the silence this key spent a release refused for.
// Matching on the message would work until somebody reworded it.
var ErrTooFine = errors.New("finer than a thousandth of a core")

// The units a resources block may be written in.
//
// They are Kubernetes' units, deliberately, because that is the vocabulary the
// people who write this key already have and inventing a second spelling of
// "512Mi" would mean a manifest that reads like a Deployment and means
// something else. The JSON schema constrains the same shapes, so a manifest
// that reaches these functions has usually already been through that; these
// are what decide, because a manifest may arrive from a caller that never
// validated against the schema at all.
const (
	// MinMilliCPU is the smallest CPU share a service may ask for.
	//
	// One thousandth of a core, which is Kubernetes' own resolution and the
	// finest thing Docker's NanoCPUs can express without rounding to nothing.
	// Below it the value is not a small request, it is a typo.
	MinMilliCPU = 1
	// MinMemoryBytes is the smallest memory cap a service may ask for.
	//
	// Six megabytes, which is the floor the Docker daemon itself enforces:
	// anything under it is refused by the daemon with a message about the
	// minimum rather than about the manifest, several seconds into an af up
	// and pointing at the wrong file. Refusing it here names the key.
	MinMemoryBytes = 6 * 1024 * 1024
)

// ParseMilliCPU reads a CPU quantity into thousandths of a core.
//
// "500m" is 500, "1.5" is 1500, "2" is 2000. One function rather than a parse
// in each runtime, because two parsers are two chances to disagree about what
// a manifest means, and the disagreement would show up as one runtime
// enforcing a cap the other did not.
func ParseMilliCPU(s string) (int64, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0, fmt.Errorf("no CPU quantity")
	}
	// The shape first, because strconv.ParseFloat is far more permissive than
	// anything a manifest means. It reads "1e3" as a thousand cores, "0x1p4"
	// as sixteen, and "NaN" and "Inf" as numbers that survive every bound
	// below and then become whatever int64 conversion does with them. The
	// pattern in schemas/manifest.v1.json admits digits, one dot and an
	// optional m, and this is the same rule stated where it is enforced: the
	// two agreeing is what stops a manifest that passes the schema from
	// meaning something else here, and what stops one that never saw the
	// schema from meaning something no schema would allow.
	if !isDecimal(strings.TrimSuffix(t, "m")) {
		return 0, fmt.Errorf("%q is not a CPU quantity", s)
	}
	if milli := strings.HasSuffix(t, "m"); milli {
		n, err := strconv.ParseFloat(strings.TrimSuffix(t, "m"), 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("%q is not a CPU quantity", s)
		}
		if n != float64(int64(n)) {
			return 0, fmt.Errorf("%q is %w", s, ErrTooFine)
		}
		return int64(n), nil
	}
	cores, err := strconv.ParseFloat(t, 64)
	if err != nil || cores < 0 {
		return 0, fmt.Errorf("%q is not a CPU quantity", s)
	}
	// Rounded rather than truncated, and then checked, so that 0.0005 is
	// reported as too fine rather than silently becoming zero. A cap that
	// rounds to nothing is the same silence this key was refused for.
	milli := int64(cores*1000 + 0.5)
	if float64(milli) != cores*1000 {
		return 0, fmt.Errorf("%q is %w", s, ErrTooFine)
	}
	return milli, nil
}

// isDecimal reports whether a string is digits with at most one dot, which is
// the whole of what a CPU quantity may look like.
//
// Written out rather than done with a regexp because this runs on every
// service of every manifest the engine reads, and because the rule is short
// enough that the loop is easier to check against the schema's pattern than a
// second spelling of it would be.
func isDecimal(s string) bool {
	if s == "" {
		return false
	}
	dots, digits := 0, 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '.':
			dots++
		default:
			return false
		}
	}
	return dots <= 1 && digits > 0
}

// ParseMemoryBytes reads a memory quantity into bytes.
//
// Mi and Gi are powers of two, M and G are powers of ten, which is what those
// suffixes mean everywhere else and what somebody copying a value out of a
// Deployment expects.
//
// A unit is REQUIRED, which is the one place this deliberately refuses
// something Kubernetes accepts. "memory: 512" there is 512 bytes, and nobody
// who writes it means 512 bytes: they mean megabytes, and the container they
// get is refused by the daemon for being under its floor. The schema's pattern
// says the same thing, and the two agreeing is what stops a manifest that
// passes the schema from meaning something else here.
func ParseMemoryBytes(s string) (int64, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0, fmt.Errorf("no memory quantity")
	}
	units := []struct {
		suffix string
		scale  int64
	}{
		{"Mi", 1024 * 1024},
		{"Gi", 1024 * 1024 * 1024},
		{"M", 1000 * 1000},
		{"G", 1000 * 1000 * 1000},
	}
	for _, u := range units {
		if !strings.HasSuffix(t, u.suffix) {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSuffix(t, u.suffix), 10, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("%q is not a memory quantity", s)
		}
		if n > (1<<62)/u.scale {
			return 0, fmt.Errorf("%q is larger than any machine", s)
		}
		return n * u.scale, nil
	}
	if _, err := strconv.ParseInt(t, 10, 64); err == nil {
		return 0, fmt.Errorf("%q names no unit", s)
	}
	return 0, fmt.Errorf("%q is not a memory quantity", s)
}

// FormatMilliCPU writes a CPU quantity the way a manifest would.
//
// For a message rather than for a manifest: a shortfall reported in
// thousandths reads as a number nobody wrote, and the point of naming a
// shortfall is that the reader can find the key it came from.
func FormatMilliCPU(milli int64) string {
	if milli%1000 == 0 {
		return strconv.FormatInt(milli/1000, 10)
	}
	return strconv.FormatInt(milli, 10) + "m"
}

// FormatMemoryBytes writes a memory quantity the way a manifest would.
func FormatMemoryBytes(bytes int64) string {
	switch {
	case bytes >= 1024*1024*1024 && bytes%(1024*1024*1024) == 0:
		return strconv.FormatInt(bytes/(1024*1024*1024), 10) + "Gi"
	case bytes >= 1024*1024 && bytes%(1024*1024) == 0:
		return strconv.FormatInt(bytes/(1024*1024), 10) + "Mi"
	default:
		// Rounded up to the megabyte, because a shortfall reported as
		// 402653184 bytes is a number the reader has to do arithmetic on
		// before it means anything.
		return strconv.FormatInt((bytes+1024*1024-1)/(1024*1024), 10) + "Mi"
	}
}
