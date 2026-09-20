package pgcrash

import (
	"fmt"
	"strconv"
	"strings"
)

// Control is what pg_controldata said about the cluster.
//
// It is read from the control file rather than from a query, and that is the
// point: the control file is the cluster's own record of where recovery would
// start and whether it believes it was shut down cleanly. A query can only be
// answered by a database that is already up, which is to say already past the
// thing being measured.
type Control struct {
	// State is the cluster state, "in production" when it is serving and
	// "in crash recovery" while it is replaying.
	State string `json:"state"`
	// CheckpointLSN is the position of the latest checkpoint record.
	CheckpointLSN string `json:"checkpointLsn"`
	// RedoLSN is where recovery would start from that checkpoint. It is the
	// number the postmaster's "redo starts at" line has to agree with.
	RedoLSN string `json:"redoLsn"`
	// TimeLine is the latest checkpoint's timeline. Crash recovery does not
	// change it, so a timeline that moved means something other than a crash
	// happened.
	TimeLine int `json:"timeline"`
	// ChecksumVersion is zero when data page checksums are off. Zero is not a
	// pass: it means a torn page would not be detected, so the run says the
	// check could not be made rather than saying it was made and was clean.
	ChecksumVersion int `json:"checksumVersion"`
	// Raw is the output the fields were read from, so a reader can see the
	// evidence rather than the summary.
	Raw string `json:"-"`
}

// ChecksumsEnabled reports whether Postgres would detect a torn page.
func (c Control) ChecksumsEnabled() bool { return c.ChecksumVersion > 0 }

// InProduction reports whether the cluster believes it is serving.
func (c Control) InProduction() bool { return c.State == "in production" }

// controlFields maps the labels pg_controldata prints to what is done with
// them. Matching on the label rather than on a line number, because the set of
// lines differs between major versions and a positional read would silently
// pick up the wrong number on the next one.
const (
	labelState      = "Database cluster state:"
	labelCheckpoint = "Latest checkpoint location:"
	labelRedo       = "Latest checkpoint's REDO location:"
	labelTimeLine   = "Latest checkpoint's TimeLineID:"
	labelChecksum   = "Data page checksum version:"
)

// ParseControl reads pg_controldata's output.
//
// A missing label is an error rather than a zero value. Every assertion built
// on this compares two of these, and a zero that reads as "the checkpoint did
// not move" when it really means "the line was not found" would turn an
// unparsed output into a recovery finding against the customer's database.
func ParseControl(out string) (Control, error) {
	c := Control{Raw: out}
	var timeline, checksum string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, labelState):
			c.State = value(line, labelState)
		case strings.HasPrefix(line, labelCheckpoint):
			c.CheckpointLSN = value(line, labelCheckpoint)
		case strings.HasPrefix(line, labelRedo):
			c.RedoLSN = value(line, labelRedo)
		case strings.HasPrefix(line, labelTimeLine):
			timeline = value(line, labelTimeLine)
		case strings.HasPrefix(line, labelChecksum):
			checksum = value(line, labelChecksum)
		}
	}
	var missing []string
	if c.State == "" {
		missing = append(missing, "the cluster state")
	}
	if c.CheckpointLSN == "" {
		missing = append(missing, "the checkpoint location")
	}
	if c.RedoLSN == "" {
		missing = append(missing, "the checkpoint's redo location")
	}
	if timeline == "" {
		missing = append(missing, "the timeline")
	}
	if checksum == "" {
		missing = append(missing, "the checksum version")
	}
	if len(missing) > 0 {
		return Control{Raw: out}, fmt.Errorf(
			"pgcrash: pg_controldata's output does not carry %s, so nothing can be concluded from it",
			strings.Join(missing, ", "))
	}
	n, err := strconv.Atoi(timeline)
	if err != nil {
		return Control{Raw: out}, fmt.Errorf("pgcrash: the timeline is %q, which is not a number", timeline)
	}
	c.TimeLine = n
	v, err := strconv.Atoi(checksum)
	if err != nil {
		return Control{Raw: out}, fmt.Errorf("pgcrash: the checksum version is %q, which is not a number", checksum)
	}
	c.ChecksumVersion = v
	return c, nil
}

// value is the part of a pg_controldata line after its label.
func value(line, label string) string {
	return strings.TrimSpace(strings.TrimPrefix(line, label))
}

// ParseLSN turns a Postgres log sequence number into the integer it is.
//
// The text form is two hexadecimal halves separated by a slash, and the halves
// are not the same width, so comparing the strings compares 0/9FFFFFF above
// 0/10000000 and every LSN assertion built on that is wrong in exactly the
// window where a crash happens.
func ParseLSN(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	slash := strings.IndexByte(s, '/')
	if slash <= 0 || slash == len(s)-1 {
		return 0, fmt.Errorf("pgcrash: %q is not a log sequence number", s)
	}
	hi, err := strconv.ParseUint(s[:slash], 16, 32)
	if err != nil {
		return 0, fmt.Errorf("pgcrash: the high half of %q is not hexadecimal", s)
	}
	lo, err := strconv.ParseUint(s[slash+1:], 16, 32)
	if err != nil {
		return 0, fmt.Errorf("pgcrash: the low half of %q is not hexadecimal", s)
	}
	return hi<<32 | lo, nil
}

// FormatLSN renders a log sequence number the way Postgres prints it.
func FormatLSN(v uint64) string {
	return fmt.Sprintf("%X/%X", v>>32, v&0xFFFFFFFF)
}
