// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds

// Whose resources these are, and whether one of them is ready to be handed out.
//
// A tag saying antifailure is not ownership. Anybody who can tag a resource in
// the account can write that tag, a second source instance in the same account
// writes the same tag through its own provider, and a retry after a lost
// response can find an instance at the right name that a different attempt
// created. So three things are bound here that a tag alone cannot bind:
//
//   - SCOPE. Every resource carries a digest of the source instance's ARN, and
//     the provider compares its own. The ARN names the partition, the region
//     and the account, so a resource from another source, another account or
//     another region is never listed, adopted or deleted by this one.
//   - PREPARATION. A branch instance or a golden snapshot carries an HMAC,
//     under the branch key, of its scope, its identifier, its version and its
//     environment. Only a provider holding the key can write one, and the tag
//     is written only after the inherited logins are gone, so a resource that
//     was interrupted between the restore and that step is never handed out.
//   - ATTEMPT. A restore carries a random nonce, so a response lost in transit
//     can be reconciled against the instance that exists without deleting a
//     resource somebody else created at the same name.

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// The tag keys this file adds to the ones rds.go owns.
const (
	tagScope    = "antifailure:source"
	tagPrepared = "antifailure:prepared"
	tagAttempt  = "antifailure:attempt"
)

// bindSource records the source instance's identity and networking.
//
// Read from the source rather than configured. A branch restored into a
// different subnet group or security group than production's is either
// unreachable from where the environment runs or reachable from somewhere
// production is not, and neither is a thing a manifest should be able to
// choose by accident.
func (p *Provider) bindSource(in dbInstance) error {
	parts := strings.Split(in.ARN, ":")
	if len(parts) != 7 || parts[0] != "arn" || parts[2] != "rds" || parts[3] != p.api.region ||
		len(parts[4]) != 12 || parts[5] != "db" || parts[6] != in.Identifier {
		return fmt.Errorf("rds: the source instance returned an invalid account, region or instance identity")
	}
	for _, ch := range parts[4] {
		if ch < '0' || ch > '9' {
			return fmt.Errorf("rds: the source instance returned an invalid account")
		}
	}
	if parts[1] != "aws" && parts[1] != "aws-us-gov" {
		return fmt.Errorf("rds: unsupported AWS partition %q", parts[1])
	}
	if in.SubnetGroup.Name == "" || len(in.SecurityGroups) == 0 {
		return fmt.Errorf("rds: the source instance's networking identity is incomplete, " +
			"so a restore could not be placed where production is")
	}
	groups := make([]string, 0, len(in.SecurityGroups))
	for _, sg := range in.SecurityGroups {
		if sg.ID == "" {
			return fmt.Errorf("rds: the source instance reports an empty security group")
		}
		groups = append(groups, sg.ID)
	}
	p.api.subnetGroup = in.SubnetGroup.Name
	p.api.securityGroups = groups
	p.source = in.Identifier
	p.partition = parts[1]
	p.arnPrefix = strings.Join(parts[:5], ":") + ":"
	digest := sha256.Sum256([]byte(in.ARN))
	p.scope = hex.EncodeToString(digest[:])
	return nil
}

// ownsInstance reports whether an instance is one this provider created for
// this source.
func (p *Provider) ownsInstance(in dbInstance) bool {
	tags := tagMap(in.Tags)
	return p.scope != "" && in.ARN == p.arnPrefix+"db:"+in.Identifier &&
		tags[tagMarker] == Name && tags[tagScope] == p.scope && in.Identifier != p.source
}

// ownsSnapshot reports whether a snapshot is one this provider created for
// this source.
func (p *Provider) ownsSnapshot(s dbSnapshot) bool {
	tags := tagMap(s.Tags)
	return p.scope != "" && s.ARN == p.arnPrefix+"snapshot:"+s.Identifier &&
		tags[tagMarker] == Name && tags[tagScope] == p.scope
}

// receipt is the preparation tag for one resource.
//
// Length prefixed, so that no two different tuples of fields can render the
// same bytes and therefore the same receipt.
func (p *Provider) receipt(kind, identifier string, tags map[string]string) string {
	h := hmac.New(sha256.New, []byte(p.branchKey.Reveal()))
	for _, s := range []string{"rds/prepared/v1", kind, p.scope, identifier, tags[tagVersion], tags[tagEnv]} {
		_, _ = fmt.Fprintf(h, "%d:%s", len(s), s)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// preparedInstance reports whether a branch instance may be handed out.
func (p *Provider) preparedInstance(in dbInstance) bool {
	tags := tagMap(in.Tags)
	return p.ownsInstance(in) && tags[tagKind] == kindBranch && in.Status == "available" &&
		!in.IAMEnabled && !in.PubliclyAccessible &&
		hmac.Equal([]byte(tags[tagPrepared]), []byte(p.receipt(kindBranch, in.Identifier, tags)))
}

// preparedSnapshot reports whether a golden snapshot may be branched from.
func (p *Provider) preparedSnapshot(s dbSnapshot) bool {
	tags := tagMap(s.Tags)
	return p.ownsSnapshot(s) && tags[tagKind] == kindGolden && s.Status == "available" &&
		hmac.Equal([]byte(tags[tagPrepared]), []byte(p.receipt(kindGolden, s.Identifier, tags)))
}

// markPrepared writes a branch instance's receipt and reads it back.
//
// Read back rather than trusted, because a control plane that accepted the tag
// request and recorded nothing would otherwise hand the engine a branch that
// every later process refuses to connect to, with no error at the moment the
// problem happened.
func (p *Provider) markPrepared(ctx context.Context, in dbInstance) error {
	tags := tagMap(in.Tags)
	if err := p.api.addTags(ctx, in.ARN, map[string]string{
		tagPrepared: p.receipt(kindBranch, in.Identifier, tags),
	}); err != nil {
		return err
	}
	actual, found, err := p.api.describeInstance(ctx, in.Identifier)
	if err != nil {
		return err
	}
	if !found || !p.preparedInstance(actual) {
		return fmt.Errorf("rds: the preparation receipt for %s was not recorded", in.Identifier)
	}
	return nil
}

// admissions serialises branch creation per source within one process, so the
// branch limit is counted and then used without a second provider for the same
// source counting the same headroom in between. Independent processes still
// rely on the account's own quota: MaxBranches is a ceiling this process keeps,
// not a lock across machines.
var admissions sync.Map

func (p *Provider) admit(ctx context.Context) (func(), error) {
	value, _ := admissions.LoadOrStore(p.scope, make(chan struct{}, 1))
	ch := value.(chan struct{})
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// acceptedResponseError is a request AWS answered with success whose body
// could not be read, so the resource was created and its description is lost.
type acceptedResponseError struct{ error }

// uncertainResponseError is a request whose response never arrived, so whether
// AWS acted on it is unknown.
type uncertainResponseError struct{ error }

func (e *acceptedResponseError) Unwrap() error  { return e.error }
func (e *uncertainResponseError) Unwrap() error { return e.error }

// restore asks for an instance from a snapshot, bound to a fresh attempt.
//
// It reports whether the instance this attempt asked for exists, which is a
// different question from whether the call succeeded: after a lost response
// the instance may be there and be ours, or be there and be somebody else's.
// Only the attempt nonce tells those apart, and only the first is cleaned up.
func (p *Provider) restore(ctx context.Context, instance, snapshot string, tags map[string]string) (bool, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return false, err
	}
	tags[tagScope] = p.scope
	tags[tagAttempt] = hex.EncodeToString(nonce[:])
	_, err := p.api.restoreFromSnapshot(ctx, instance, snapshot, p.instanceClass, tags)
	if err == nil {
		return true, nil
	}
	if !uncertainCreation(err) {
		return false, err
	}
	check, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	in, found, lookupErr := p.api.describeInstance(check, instance)
	if lookupErr == nil && found && p.ownsInstance(in) && tagMap(in.Tags)[tagAttempt] == tags[tagAttempt] {
		return true, err
	}
	return false, fmt.Errorf("rds: whether AWS created %s is unresolved; inspect its attempt tag "+
		"before removing anything at that name: %w", instance, err)
}

// uncertainCreation reports whether an error leaves a creation unresolved.
func uncertainCreation(err error) bool {
	var a *acceptedResponseError
	var u *uncertainResponseError
	return errors.As(err, &a) || errors.As(err, &u)
}
