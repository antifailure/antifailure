package env

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/volume"
)

// The volume profile: reading the committed one, and recording a new one.
//
// The reading half has no credential and reaches nothing. The manifest names a
// file, the file was collected once from a read only connection to production
// or a replica, and every machine afterwards, including a pull request check
// that can reach nothing at all, reads the file. That is what makes a
// denominator available on the machine that needs one.
//
// The recording half is the only part that touches production, and it touches
// it the way `af golden refresh` does: on a machine that already holds the
// credential, over the connection the manifest already names, reading no row.
// Every figure it collects comes from a catalog the planner maintains.

// volumeProfile reads the committed profile, or says why there is none.
//
// A reason rather than an error, because every caller is a report and a report
// that refuses to say anything because one input was stale tells the reader
// less than one that says which input was stale. Nothing substitutes a number
// for the reason.
func (o *Orchestrator) volumeProfile() (*volume.Profile, string) {
	db := o.opts.Manifest.Database
	if db == nil || db.Volume == nil || strings.TrimSpace(db.Volume.Profile) == "" {
		return nil, "no volume profile says what production holds, so whether this branch " +
			"reproduces it is unknown. Declare one under database.volume and record it with " +
			"af volume record"
	}
	maxAge, err := manifest.ParseDuration(db.Volume.MaxAge)
	if err != nil {
		return nil, fmt.Sprintf(
			"database.volume.max_age is %q, which is not a duration, so nothing could decide "+
				"whether the profile is still production's", db.Volume.MaxAge)
	}
	profile, why := volume.Load(
		filepath.Join(o.opts.Root, db.Volume.Profile), maxAge, o.opts.Clock.Now())
	if why != "" {
		return nil, why
	}
	return &profile, ""
}

// VolumeProfilePath is where the manifest says the profile lives, and false
// when it declares none.
func (o *Orchestrator) VolumeProfilePath() (string, bool) {
	db := o.opts.Manifest.Database
	if db == nil || db.Volume == nil || strings.TrimSpace(db.Volume.Profile) == "" {
		return "", false
	}
	return filepath.Join(o.opts.Root, db.Volume.Profile), true
}

// VolumeProfile is the committed profile, and the reason there is none.
func (o *Orchestrator) VolumeProfile() (*volume.Profile, string) { return o.volumeProfile() }

// VolumeMaxAge is how old the manifest lets a profile get, and zero when it
// declares no volume block or an age nothing can parse.
//
// Zero is not "never stale" by accident here: manifest.ParseDuration only
// fails on a value the validator already refused, and volumeProfile turns that
// same value into a reason of its own rather than reading the profile at all.
func (o *Orchestrator) VolumeMaxAge() time.Duration {
	db := o.opts.Manifest.Database
	if db == nil || db.Volume == nil {
		return 0
	}
	d, err := manifest.ParseDuration(db.Volume.MaxAge)
	if err != nil {
		return 0
	}
	return d
}

// RecordVolume reads production's shape and returns the profile.
//
// The source is database.source_url_env, the same variable the golden refresh
// reads, because it is the same database and asking somebody to configure a
// second name for it is how the two drift apart. A project with no source
// configured is refused by name rather than given an empty profile: a profile
// of nothing would be a denominator of zero, and a denominator of zero is how
// a report ends up claiming a branch reproduces everything.
func (o *Orchestrator) RecordVolume(ctx context.Context) (volume.Profile, error) {
	db := o.opts.Manifest.Database
	if db == nil || strings.TrimSpace(db.SourceURLEnv) == "" {
		return volume.Profile{}, errors.New(
			"database.source_url_env names no variable, so there is no production database " +
				"to read a volume profile from. Set it to the variable holding a read only " +
				"connection string, to production or to a replica of it")
	}
	url, err := o.sourceURL(ctx)
	if err != nil {
		return volume.Profile{}, err
	}
	if url.IsZero() {
		return volume.Profile{}, fmt.Errorf(
			"%s holds no value, so there is no production database to read a volume profile "+
				"from. Export it, or store it with af secret set", db.SourceURLEnv)
	}

	conn, err := pgx.Connect(ctx, url.Reveal())
	if err != nil {
		// The host and not the connection string. AF-DB-002 names where it
		// tried to reach, which is the one part of a production DSN that is
		// not a credential, and the value itself is registered with the
		// redactor before it gets here anyway.
		return volume.Profile{}, aferrors.Wrap(err, aferrors.AFDB002, "host", hostOf(url.Reveal()))
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	// The variable's NAME, never its value. A profile is committed, so a
	// connection string in it would be a credential in the repository.
	return volume.Collect(ctx, conn, "the database named by "+db.SourceURLEnv, o.opts.Clock.Now())
}

// hostOf pulls the host out of a connection string, for an error message.
//
// The parse is the point rather than an optimisation: string surgery on a DSN
// is how a password ends up in a log, and a DSN that will not parse has no
// host to name, so it says so instead of printing what it was given.
func hostOf(dsn string) string {
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil || cfg.Host == "" {
		return "the address database.source_url_env names"
	}
	if cfg.Port != 0 {
		return fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	}
	return cfg.Host
}
