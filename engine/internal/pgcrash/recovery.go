package pgcrash

import (
	"regexp"
	"strconv"
	"strings"
)

// Recovery is what the postmaster's own log says happened.
//
// The log is read rather than inferred, and every field here is a line
// Postgres wrote about itself. The alternative, which is what a recovery
// check usually is, is to reconnect afterwards and report that the connection
// succeeded. That reports the same thing whether the database crashed and
// replayed its write ahead log or whether the fault never landed at all, and a
// check that answers the same on both is not a check.
type Recovery struct {
	// Crashed is true when the log carries a process dying on a signal.
	Crashed bool `json:"crashed"`
	// Signal is the signal number that killed it.
	Signal int `json:"signal,omitempty"`
	// CrashLine is the line that said so.
	CrashLine string `json:"crashLine,omitempty"`
	// Reinitialised is true when the postmaster said it was discarding shared
	// memory and starting the server processes again. This is what separates
	// a crash from one backend disconnecting.
	Reinitialised bool `json:"reinitialised"`
	// Unclean is true when the startup process said the cluster was not shut
	// down properly, which is the line that precedes replay.
	Unclean bool `json:"unclean"`
	// UncleanLine is the line that said so.
	UncleanLine string `json:"uncleanLine,omitempty"`
	// RedoStart and RedoEnd are the positions replay began and ended at. They
	// are empty when the log said redo was not required.
	RedoStart string `json:"redoStart,omitempty"`
	RedoEnd   string `json:"redoEnd,omitempty"`
	// RedoNotRequired is true when the cluster came back and had nothing to
	// replay. It is a real outcome and it is recorded separately, because a
	// run that expected replay and got none has not proved what it set out to.
	RedoNotRequired bool `json:"redoNotRequired"`
	// Ready is true when the database said it was accepting connections again.
	Ready bool `json:"ready"`
	// Lines is the part of the log these were read from, for the report.
	Lines []string `json:"lines,omitempty"`
}

// Replayed reports whether the write ahead log was actually replayed.
func (r Recovery) Replayed() bool { return r.RedoStart != "" && r.RedoEnd != "" }

// Patterns Postgres writes around a crash. They are the server's own English
// and they have been stable across major versions for a long time, which is
// why they are matched at all; each one is paired with a second signal from a
// different source in Verify, so a reworded message downgrades a run to
// unverified rather than turning it green.
var (
	reCrash    = regexp.MustCompile(`was terminated by signal (\d+)`)
	reRedoAt   = regexp.MustCompile(`redo (starts|done) at ([0-9A-Fa-f]+/[0-9A-Fa-f]+)`)
	reInterest = regexp.MustCompile(`terminated by signal|reinitializing|not properly shut down|was interrupted|redo starts at|redo done at|redo is not required|ready to accept connections|automatic recovery in progress|end-of-recovery|database system is shut down`)
)

const (
	phraseReinit    = "all server processes terminated; reinitializing"
	phraseUnclean   = "was not properly shut down; automatic recovery in progress"
	phraseInterrupt = "database system was interrupted"
	phraseNoRedo    = "redo is not required"
	phraseReady     = "database system is ready to accept connections"
)

// interestingLines is how many matched lines are kept.
//
// A crash and its recovery are a dozen lines. This is enough headroom for a
// noisy cluster and small enough that the whole set fits in a report somebody
// will read.
const interestingLines = 40

// ParseRecovery reads a postmaster log for the evidence of a crash and the
// replay that followed it.
//
// The log handed in must already be windowed to after the fault. Reading a
// whole container's log would find the phrases from the initdb that ran when
// the image was built, and a recovery check that passes on the log of a
// database starting for the first time is a recovery check that never ran.
func ParseRecovery(log string) Recovery {
	var r Recovery
	for _, line := range strings.Split(log, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || !reInterest.MatchString(line) {
			continue
		}
		if len(r.Lines) < interestingLines {
			r.Lines = append(r.Lines, line)
		}
		if m := reCrash.FindStringSubmatch(line); m != nil && !r.Crashed {
			r.Crashed = true
			r.CrashLine = line
			if n, err := strconv.Atoi(m[1]); err == nil {
				r.Signal = n
			}
		}
		if strings.Contains(line, phraseReinit) {
			r.Reinitialised = true
		}
		if strings.Contains(line, phraseUnclean) || strings.Contains(line, phraseInterrupt) {
			r.Unclean = true
			if r.UncleanLine == "" {
				r.UncleanLine = line
			}
		}
		if strings.Contains(line, phraseNoRedo) {
			r.RedoNotRequired = true
		}
		if strings.Contains(line, phraseReady) {
			r.Ready = true
		}
		// Last wins for both positions. A container restarted more than once
		// inside the window would otherwise report the first replay, and the
		// assertion is about the replay that brought the database back.
		for _, m := range reRedoAt.FindAllStringSubmatch(line, -1) {
			if m[1] == "starts" {
				r.RedoStart = m[2]
			} else {
				r.RedoEnd = m[2]
			}
		}
	}
	return r
}
