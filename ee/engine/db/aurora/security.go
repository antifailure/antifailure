// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
package aurora

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

const tagScope = "antifailure:source"
const tagPrepared = "antifailure:prepared"
const tagAttempt = "antifailure:attempt"

func (p *Provider) bindSource(c dbCluster) error {
	parts := strings.Split(c.ARN, ":")
	if len(parts) != 7 || parts[0] != "arn" || parts[2] != "rds" || parts[3] != p.api.region || len(parts[4]) != 12 || parts[5] != "cluster" || parts[6] != c.Identifier {
		return fmt.Errorf("aurora: source returned an invalid account, region or cluster identity")
	}
	for _, ch := range parts[4] {
		if ch < '0' || ch > '9' {
			return fmt.Errorf("aurora: invalid source account")
		}
	}
	if parts[1] != "aws" && parts[1] != "aws-us-gov" {
		return fmt.Errorf("aurora: unsupported AWS partition %q", parts[1])
	}
	if c.Subnet == "" || len(c.SecurityGroups) == 0 {
		return fmt.Errorf("aurora: source networking identity is incomplete")
	}
	p.api.subnet = c.Subnet
	for _, sg := range c.SecurityGroups {
		if sg.ID == "" {
			return fmt.Errorf("aurora: source security group is empty")
		}
		p.api.securityGroups = append(p.api.securityGroups, sg.ID)
	}
	p.source = c.Identifier
	p.partition = parts[1]
	p.arnPrefix = strings.Join(parts[:6], ":") + ":"
	digest := sha256.Sum256([]byte(c.ARN))
	p.scope = hex.EncodeToString(digest[:])
	return nil
}

func (p *Provider) owned(c dbCluster) bool {
	return p.scope != "" && c.ARN == p.arnPrefix+c.Identifier && c.tags()[tagMarker] == Name && c.tags()[tagScope] == p.scope && c.Identifier != p.source
}
func (p *Provider) branchName(env string) string { return branchPrefix + shortHash(p.scope+"\n"+env) }
func (p *Provider) receipt(c dbCluster) string {
	t := c.tags()
	h := hmac.New(sha256.New, []byte(p.branchKey.Reveal()))
	for _, s := range []string{"aurora/prepared/v1", p.scope, c.Identifier, t[tagVersion], t[tagEnv]} {
		_, _ = fmt.Fprintf(h, "%d:%s", len(s), s)
	}
	return hex.EncodeToString(h.Sum(nil))
}
func (p *Provider) prepared(c dbCluster) bool {
	return p.owned(c) && !c.IAMEnabled && c.Status == "available" && c.writer() != "" && hmac.Equal([]byte(c.tags()[tagPrepared]), []byte(p.receipt(c)))
}
func (p *Provider) markPrepared(ctx context.Context, c dbCluster) error {
	if err := p.api.setTags(ctx, c.ARN, map[string]string{tagPrepared: p.receipt(c)}); err != nil {
		return err
	}
	actual, found, err := p.api.describeCluster(ctx, c.Identifier)
	if err != nil {
		return err
	}
	if !found || !p.prepared(actual) {
		return fmt.Errorf("aurora: preparation receipt was not recorded")
	}
	return nil
}

// Admission is shared by provider instances in this process. Independent
// engines still rely on AWS quotas; MaxBranches is not a global hard quota.
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

type acceptedResponseError struct{ error }
type uncertainResponseError struct{ error }

func (e *acceptedResponseError) Unwrap() error  { return e.error }
func (e *uncertainResponseError) Unwrap() error { return e.error }

// Tags are atomic with restore. A unique attempt receipt lets a lost response
// be reconciled without deleting a pre-existing resource at the same name.
func (p *Provider) clone(ctx context.Context, source, target string, tags map[string]string) (bool, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return false, err
	}
	tags[tagScope] = p.scope
	tags[tagAttempt] = hex.EncodeToString(nonce[:])
	_, err := p.api.cloneCluster(ctx, source, target, tags)
	if err == nil {
		return true, nil
	}
	var accepted *acceptedResponseError
	var uncertain *uncertainResponseError
	if !errors.As(err, &accepted) && !errors.As(err, &uncertain) {
		return false, err
	}
	check, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	c, found, lookupErr := p.api.describeCluster(check, target)
	if lookupErr == nil && found && p.owned(c) && c.tags()[tagAttempt] == tags[tagAttempt] {
		return true, err
	}
	return false, fmt.Errorf("aurora: creation acceptance is unresolved for %s; inspect its attempt receipt before cleanup: %w", target, err)
}

func uncertainCreation(err error) bool {
	var a *acceptedResponseError
	var u *uncertainResponseError
	return errors.As(err, &a) || errors.As(err, &u)
}
