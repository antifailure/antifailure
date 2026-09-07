package volume

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// The artifact, and the one rule that makes it worth having.
//
// A volume profile is committed. It has to be, because the machine running `af
// fidelity` on a pull request cannot reach production and the denominator has
// to come from somewhere. A committed number goes out of date silently, and a
// number that has gone out of date silently is exactly the thing this
// repository keeps finding in its own instruments.
//
// So a stale profile is REFUSED, the way golden.max_age already refuses a
// stale golden, and for the same reason stated the same way: an environment
// branched from a golden nobody refreshed is testing data production has moved
// on from, and a fidelity report quoting a profile nobody refreshed is quoting
// a row count production has moved on from. Neither is reported as a smaller
// pass. Both say they could not be measured.

// DefaultMaxAge is how old a profile may be before it is refused.
//
// Thirty days rather than the golden's seven. A golden is the data and goes
// out of date as fast as the data does; a profile is the SHAPE of the data,
// which moves at the rate a business grows rather than at the rate rows are
// written. Seven days here would refuse a perfectly good denominator every
// week and teach somebody to widen it to a year, which is the failure the
// setting exists to prevent.
const DefaultMaxAge = 30 * 24 * time.Hour

// ErrNoProfile is returned when the manifest names a profile and the file is
// not there.
var ErrNoProfile = errors.New("no volume profile")

// Write saves a profile.
//
// Indented, because it is committed and a diff of one line per table is the
// point: somebody reviewing a refresh should be able to see which table grew.
func Write(path string, p Profile) error {
	body, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	return os.WriteFile(path, append(body, '\n'), 0o600)
}

// Read loads a profile from disk.
func Read(path string) (Profile, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Profile{}, fmt.Errorf("%w at %s", ErrNoProfile, path)
	}
	if err != nil {
		return Profile{}, err
	}
	var p Profile
	if err := json.Unmarshal(body, &p); err != nil {
		return Profile{}, fmt.Errorf("%s is not a volume profile: %w", path, err)
	}
	return p, nil
}

// Load reads a profile and refuses one too old to quote.
//
// The reason is returned rather than an error, because everything that reads a
// profile is a report, and a report that refuses to say anything because one
// input was stale tells the reader less than one that says which input was
// stale. Every caller puts the reason in front of somebody instead of
// substituting a number for it.
//
// A maxAge of zero is not a maximum age and never refuses anything, which
// matches golden.Stale exactly: a project that has not configured one is never
// interrupted by a refresh it did not ask for.
func Load(path string, maxAge time.Duration, now time.Time) (Profile, string) {
	p, err := Read(path)
	switch {
	case errors.Is(err, ErrNoProfile):
		return Profile{}, "no volume profile has been recorded at " + path +
			", so nothing says what production holds. Record one with af volume record"
	case err != nil:
		return Profile{}, "the volume profile at " + path + " could not be read: " + oneLine(err)
	}
	if p.CollectedAt.IsZero() {
		// Refused rather than treated as fresh. A profile that does not say
		// when it was taken is the one case where the age cannot be checked at
		// all, and an unknown age is the most stale a profile can be.
		return Profile{}, "the volume profile at " + path +
			" does not say when it was collected, so nothing can say whether it is still production's"
	}
	if Stale(p.CollectedAt, maxAge, now) {
		return Profile{}, fmt.Sprintf(
			"the volume profile at %s was collected %s ago, past the %s it is allowed to be, "+
				"so production's row counts are not known. Record a new one with af volume record",
			path, age(now.Sub(p.CollectedAt)), age(maxAge))
	}
	return p, ""
}

// Stale reports whether a profile is old enough that quoting it is quoting a
// row count production has moved on from.
//
// The same shape as golden.Stale, deliberately: a zero collection time is
// stale, and a maximum age of zero is not a maximum age.
func Stale(collected time.Time, maxAge time.Duration, now time.Time) bool {
	if maxAge <= 0 {
		return false
	}
	if collected.IsZero() {
		return true
	}
	return now.Sub(collected) > maxAge
}

// age renders a duration in the unit somebody would say it in.
func age(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	case d >= 2*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	case d >= 2*time.Minute:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	}
	return d.Round(time.Second).String()
}
