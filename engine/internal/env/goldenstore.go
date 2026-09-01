package env

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/antifailure/antifailure/engine/internal/db/pgcopy"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/golden"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/internal/verify"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// Publishing a golden somewhere other than the machine that made it is what
// makes a fleet possible. One machine holds the production credential and does
// the refresh; every runner pulls what it published and never reads production
// at all.
//
// Two objects per version, and the ORDER they are written in is the contract.
// The dump goes first and the attestation second, so a version that has one
// and not the other is a run that died partway and is invisible to everything
// that lists the store. A version is complete when its attestation is there,
// which is also the only thing that makes it a golden rather than a file
// somebody could have put anything in.

// goldenStore opens the store this project publishes to, or nil when it
// publishes to nowhere, which is the default and is not an error.
func (o *Orchestrator) goldenStore() (golden.Store, error) {
	db := o.opts.Manifest.Database
	if db == nil || db.Golden == nil {
		return nil, nil
	}
	getenv := o.opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	s, err := golden.OpenStore(golden.Kind(db.Golden.Storage), db.Golden.StorageURL, getenv)
	if err != nil {
		return nil, aferrors.Coded(aferrors.AFDB011, "detail", err.Error())
	}
	return s, nil
}

// publishGolden copies a version's dump and attestation into the store.
//
// The dump comes from a BRANCH of the published version rather than from the
// candidate that made it. The candidate is gone by this point, deliberately:
// the provider removes it as soon as it has committed, so that a failed
// refresh cannot leave a branchable copy of unmasked production behind. A
// branch of the published golden holds exactly what was published, which is
// also the only thing worth publishing.
func (o *Orchestrator) publishGolden(
	ctx context.Context, s *session, store golden.Store, gv provider.GoldenVersion, attestation string,
) error {
	if store == nil || gv.ID == "" {
		return nil
	}
	o.progress("publishing " + gv.ID + " to " + store.Name())

	branch, err := s.dbProv.Branch(ctx, gv.ID, o.envID+"-publish")
	if err != nil {
		return err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		// Removed whether or not the upload worked. A branch that outlives the
		// publish is a copy of the data nobody is watching.
		_ = s.dbProv.Destroy(c, branch)
	}()

	url, err := s.dbProv.ConnString(ctx, branch, provider.ConnDirect)
	if err != nil {
		return err
	}
	o.opts.Redactor.Register(url.Reveal())

	// Buffered rather than streamed into the upload, because the upload needs
	// a length: an Azure block blob PUT with an unknown length becomes a
	// chunked request and the service refuses those. A subset sized dump is
	// what this feature exists to produce; a caller dumping the whole of
	// production should be told rather than quietly held in memory, and the
	// ceiling below is where they are told.
	var buf bytes.Buffer
	if err := pgcopy.DumpTo(ctx, url, &buf); err != nil {
		return aferrors.Coded(aferrors.AFDB011, "detail", err.Error())
	}
	if buf.Len() > maxPublishBytes {
		return aferrors.Coded(aferrors.AFDB011, "detail", fmt.Sprintf(
			"the dump of %s is %d MiB and this publishes through memory, which is bounded at %d MiB. "+
				"Configure database.subset so the golden is a slice rather than the whole database, "+
				"or leave database.golden.storage_url unset and keep goldens on the provider",
			gv.ID, buf.Len()/(1<<20), maxPublishBytes/(1<<20)))
	}

	if err := store.Put(ctx, golden.DumpName(gv.ID), int64(buf.Len()), bytes.NewReader(buf.Bytes())); err != nil {
		return aferrors.Coded(aferrors.AFDB011, "detail", err.Error())
	}
	// Second, and only second. Until this lands the version is invisible to
	// anything that lists the store.
	body := []byte(attestation)
	if len(body) == 0 {
		body = []byte(`{}`)
	}
	if err := store.Put(ctx, golden.AttestationName(gv.ID), int64(len(body)), bytes.NewReader(body)); err != nil {
		return aferrors.Coded(aferrors.AFDB011, "detail", err.Error())
	}
	o.progress(fmt.Sprintf("published %s (%d MiB)", gv.ID, buf.Len()/(1<<20)))
	return nil
}

// maxPublishBytes is where a publish refuses rather than swallowing the
// machine. Generous for a subset, and small enough that the refusal arrives
// before the swap does.
const maxPublishBytes = 8 << 30

// PullResult is what a pull produced.
type PullResult struct {
	// Version is the identifier the golden has on THIS machine, which is a new
	// one. A version identifier carries the time it was made and the rules
	// hash it was made with, and the copy on this machine was made now.
	Version string
	// From is the identifier it had in the store.
	From     string
	Verified bool
	Report   verify.Report
	Bytes    int64
}

// PullGolden brings a published golden onto this machine.
//
// The order is the same guarantee the refresh path makes, with the copy step
// replaced: restore, verify, and only then is it a golden anything can branch.
// It is NOT trusted because it came from the store. The attestation travels
// with it, but an attestation is a statement about a database and the database
// is the thing that arrived, so the verification scan runs here too, against
// what actually landed. A pull that skipped it would make the store a way to
// get an unverified database branched, which is the one thing the whole
// product refuses.
func (o *Orchestrator) PullGolden(ctx context.Context, version string) (*PullResult, error) {
	s, err := o.open(ctx, "af golden pull")
	if err != nil {
		return nil, err
	}
	defer s.close()
	return o.pullWithin(ctx, s, version)
}

func (o *Orchestrator) pullWithin(ctx context.Context, s *session, version string) (*PullResult, error) {
	store, err := o.goldenStore()
	if err != nil {
		return nil, err
	}
	if store == nil {
		return nil, aferrors.Coded(aferrors.AFDB011, "detail",
			"this project publishes goldens nowhere, so there is nothing to pull. "+
				"Set database.golden.storage and database.golden.storage_url")
	}
	if !s.dbProv.Capabilities().Subsetting {
		// The same capability, for the same reason: a pull loads a dump into
		// an empty candidate, and a provider whose candidate is a branch of
		// production has nowhere to put one.
		return nil, aferrors.Coded(aferrors.AFDB011, "detail", fmt.Sprintf(
			"the provider %q builds a golden by branching the source rather than by filling "+
				"an empty database, so a published dump has nowhere to go", s.dbProv.Name()))
	}

	prov, err := o.provenanceOf()
	if err != nil {
		return nil, err
	}

	if version == "" {
		available, listErr := golden.VersionsIn(ctx, store)
		if listErr != nil {
			return nil, aferrors.Coded(aferrors.AFDB011, "detail", listErr.Error())
		}
		if len(available) == 0 {
			return nil, aferrors.Coded(aferrors.AFDB011, "detail",
				"there are no complete versions in "+store.Name())
		}
		version = available[0].Name
	}

	attestation, err := readObject(ctx, store, golden.AttestationName(version))
	if err != nil {
		if errors.Is(err, golden.ErrNotFound) {
			return nil, aferrors.Coded(aferrors.AFDB011, "detail", fmt.Sprintf(
				"%s holds no attestation for %s, so either it was never published or the "+
					"publish did not finish. A dump with nothing to check it against is not a golden",
				store.Name(), version))
		}
		return nil, aferrors.Coded(aferrors.AFDB011, "detail", err.Error())
	}

	// Whose golden this is, before a byte of it is restored.
	//
	// A store is shared on purpose, which is the whole point of publishing,
	// and with no version named this takes the newest object in it. Without
	// this check that is the same defect as the local one one layer out: the
	// newest thing in a bucket several projects publish to is not necessarily
	// this project's, and restoring it would put another project's masked
	// production into this project's golden pool, where every later `af up`
	// would branch it as its own.
	//
	// The attestation is where the claim lives because it is the only part of
	// a published golden that travels with it and is signed. An older
	// attestation carries none, and one is refused rather than assumed to be
	// ours: this refusal costs a refresh and naming the version explicitly
	// still works, where the other way round costs somebody a preview built on
	// data that was never theirs.
	if attested := attestedProvenance(attestation); attested != prov.digest() {
		return nil, aferrors.Coded(aferrors.AFDB015,
			"version", version, "store", store.Name())
	}

	result := &PullResult{From: version}
	rulesHash := attestedRulesHash(attestation)

	gv, err := s.dbProv.RefreshGolden(ctx, provider.GoldenSpec{
		Version: databaseVersion(o.opts.Manifest),
		// The rules hash the attestation records, so that a pulled golden is
		// comparable with one refreshed here under the same rules and shows up
		// as a different one under different rules.
		RulesHash: rulesHash,
		// This project's own identity, not the one read out of the store. The
		// two are equal, because a mismatch was refused above, and computing
		// it here rather than copying it is what keeps that true: a pulled
		// golden that recorded a foreign identity would be unbranchable, and
		// one that recorded a copied identity would launder a stolen one.
		Provenance: prov.digest(),
		// Non-zero so the provider takes the Load path rather than its seed.
		SourceURL: secrets.New("published://" + version),
		Load: func(ctx context.Context, _, candidate secrets.Value) error {
			body, getErr := store.Get(ctx, golden.DumpName(version))
			if getErr != nil {
				return aferrors.Coded(aferrors.AFDB011, "detail", getErr.Error())
			}
			defer func() { _ = body.Close() }()
			counted := &countingReader{r: body}
			o.progress("restoring " + version + " from " + store.Name())
			if restoreErr := pgcopy.RestoreFrom(ctx, candidate, counted); restoreErr != nil {
				return aferrors.Coded(aferrors.AFDB011, "detail", restoreErr.Error())
			}
			result.Bytes = counted.n
			return nil
		},
		// Already masked, by the machine that published it. Masking again
		// would produce a different mapping under a different key and make the
		// attestation describe a database that no longer exists.
		Mask: func(context.Context, secrets.Value) error { return nil },
		Verify: func(ctx context.Context, url secrets.Value) (string, error) {
			report, att, verifyErr := o.verifyDatabase(ctx, s, url, rulesHash, prov.digest())
			result.Report = report
			if verifyErr != nil {
				return "", verifyErr
			}
			return att, nil
		},
	})
	result.Version, result.Verified = gv.ID, gv.Verified
	if err != nil {
		return result, err
	}
	if err := o.recordRefresh(ctx, s); err != nil {
		return result, err
	}
	return result, nil
}

// PublishedGoldens lists what the store holds.
func (o *Orchestrator) PublishedGoldens(ctx context.Context) ([]golden.Object, string, error) {
	store, err := o.goldenStore()
	if err != nil || store == nil {
		return nil, "", err
	}
	versions, err := golden.VersionsIn(ctx, store)
	if err != nil {
		return nil, store.Name(), aferrors.Coded(aferrors.AFDB011, "detail", err.Error())
	}
	return versions, store.Name(), nil
}

func readObject(ctx context.Context, store golden.Store, name string) (string, error) {
	r, err := store.Get(ctx, name)
	if err != nil {
		return "", err
	}
	defer func() { _ = r.Close() }()
	// Bounded, because an attestation is a few kilobytes and something else
	// under that name is not an attestation.
	body, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// attestedProvenance reads the identity of the project an attestation was
// signed for, and returns the empty string when it carries none.
//
// Empty is never equal to a real identity, because a real one always carries
// at least the manifest name and the major version, so an attestation written
// before this field existed is refused by the comparison rather than by a
// separate branch that somebody could later invert.
func attestedProvenance(attestation string) string {
	var doc struct {
		Provenance string `json:"provenance"`
	}
	if err := json.Unmarshal([]byte(attestation), &doc); err != nil {
		return ""
	}
	return doc.Provenance
}

// attestedRulesHash reads the rules hash out of an attestation, and returns a
// marker when it is not there rather than an error.
//
// Not an error, because the attestation is checked by re-running the
// verification scan rather than by trusting what it says, and a version that
// arrived without a readable one is still a database that either passes that
// scan or does not.
func attestedRulesHash(attestation string) string {
	var doc struct {
		RulesHash string `json:"rules_hash"`
	}
	if err := json.Unmarshal([]byte(attestation), &doc); err == nil && doc.RulesHash != "" {
		return doc.RulesHash
	}
	return "published"
}

// countingReader counts what went past, for the report.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

var _ = pgx.Connect
