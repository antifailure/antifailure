// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package cloudsql

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
	"github.com/jackc/pgx/v5/pgconn"
)

const sourceLabelKey = "antifailure-source"

const preparedLabelKey = "antifailure-prepared"
const iamAuthenticationFlag = "cloudsql.iam_authentication"

func builtinUser(u user) bool { return u.Type == "" || u.Type == "BUILT_IN" }

// selectAdministrator never picks an IAM principal for password authentication.
// The default user type is BUILT_IN in the Admin API. Unknown types are refused
// because silently skipping a new authentication mechanism can retain access.
func selectAdministrator(configured string, users []user) (string, error) {
	seen := map[string]bool{}
	var builtins []string
	var administrators []string
	for _, u := range users {
		if u.Name == "" || seen[u.Name] {
			return "", fmt.Errorf("cloudsql: the user collection contains an empty or duplicate name")
		}
		seen[u.Name] = true
		switch u.Type {
		case "", "BUILT_IN":
			builtins = append(builtins, u.Name)
			for _, role := range u.DatabaseRoles {
				if role == "cloudsqlsuperuser" {
					administrators = append(administrators, u.Name)
					break
				}
			}
		case "CLOUD_IAM_USER", "CLOUD_IAM_SERVICE_ACCOUNT", "CLOUD_IAM_GROUP", "CLOUD_IAM_GROUP_USER", "CLOUD_IAM_GROUP_SERVICE_ACCOUNT", "CLOUD_IAM_WORKFORCE_IDENTITY":
		default:
			return "", fmt.Errorf("cloudsql: user %q has unsupported authentication type %q", u.Name, u.Type)
		}
	}
	if configured != "" {
		for _, name := range builtins {
			if name == configured {
				return name, nil
			}
		}
		return "", fmt.Errorf("cloudsql: configured administrator %q is not a built-in user", configured)
	}
	for _, name := range builtins {
		if name == "postgres" {
			return name, nil
		}
	}
	if len(administrators) == 1 {
		return administrators[0], nil
	}
	if len(builtins) == 1 {
		return builtins[0], nil
	}
	return "", fmt.Errorf("cloudsql: no unambiguous built-in administrator exists; configure AdminUser explicitly")
}

// prepareCredentials removes every inherited authentication path before a
// golden is masked or a branch is published. Roles and their object ownership
// survive. Only the selected administrator's new credential is returned.
//
// Google documents both user types and the IAM flag's effect:
// https://docs.cloud.google.com/sql/docs/postgres/admin-api/rest/v1beta4/users
// https://docs.cloud.google.com/sql/docs/postgres/iam-authentication
func (p *Provider) prepareCredentials(ctx context.Context, name string) (secret.Value, error) {
	return p.prepareCredentialPolicy(ctx, name, false)
}

// Post-hook checks must not reset an unchanged password: inherited reuse and
// minimum-change-interval policies can legitimately reject that second write.
func (p *Provider) reassertCredentials(ctx context.Context, name string) (secret.Value, error) {
	return p.prepareCredentialPolicy(ctx, name, true)
}

func (p *Provider) prepareCredentialPolicy(ctx context.Context, name string, reuse bool) (secret.Value, error) {
	in, err := p.api.getInstance(ctx, name)
	if err != nil {
		return secret.Value{}, err
	}
	if !p.owns(in) {
		return secret.Value{}, fmt.Errorf("cloudsql: instance %q: %w", name, ErrNotOurs)
	}
	users, err := p.api.listUsers(ctx, name)
	if err != nil {
		return secret.Value{}, err
	}
	admin, err := selectAdministrator(p.opts.AdminUser, users)
	if err != nil {
		return secret.Value{}, err
	}
	flags := make([]databaseFlag, 0, len(in.Settings.DatabaseFlags)+1)
	for _, flag := range in.Settings.DatabaseFlags {
		if flag.Name != iamAuthenticationFlag {
			flags = append(flags, flag)
		}
	}
	flags = append(flags, databaseFlag{Name: iamAuthenticationFlag, Value: "off"})
	op, err := p.api.patchInstance(ctx, name, map[string]any{
		"settings": map[string]any{"databaseFlags": flags},
	})
	if err != nil {
		return secret.Value{}, err
	}
	if err := p.api.waitForOperation(ctx, op, p.opts.PollInterval); err != nil {
		return secret.Value{}, err
	}
	in, err = p.api.getInstance(ctx, name)
	if err != nil {
		return secret.Value{}, err
	}
	found, err := p.api.listDatabases(ctx, name)
	if err != nil {
		return secret.Value{}, err
	}
	connection, err := p.secureConnString(ctx, in, admin, p.branchPassword(name), pickDatabase(p.opts.Database, found))
	if err != nil {
		return secret.Value{}, err
	}
	rotate := true
	if reuse {
		probe, openErr := sql.Open("pgx", connection.Reveal())
		if openErr != nil {
			return secret.Value{}, openErr
		}
		authErr := probe.PingContext(ctx)
		_ = probe.Close()
		if authErr == nil {
			rotate = false
		} else {
			var databaseErr *pgconn.PgError
			if !errors.As(authErr, &databaseErr) || (databaseErr.Code != "28P01" && databaseErr.Code != "28000") {
				return secret.Value{}, fmt.Errorf("cloudsql: verifying prepared administrator: %w", authErr)
			}
		}
	}
	if rotate {
		for _, u := range users {
			if !builtinUser(u) || (reuse && u.Name != admin) {
				continue
			}
			password := p.branchPassword(name + "\x00" + u.Name)
			if u.Name == admin {
				password = p.branchPassword(name)
			}
			op, err := p.api.setPassword(ctx, name, u.Name, password)
			if err != nil {
				return secret.Value{}, fmt.Errorf("cloudsql: rotating user %q on %q: %w", u.Name, name, err)
			}
			if err := p.api.waitForOperation(ctx, op, p.opts.PollInterval); err != nil {
				return secret.Value{}, err
			}
		}
	}
	if err := p.disableInheritedLogins(ctx, connection); err != nil {
		return secret.Value{}, err
	}
	return connection, nil
}

// A preparation receipt binds the resource, source, version, environment and
// key. An ownership label alone is visible before preparation finishes and
// cannot prove that a killed worker ever rotated the inherited credentials.
func (p *Provider) preparedToken(name, version, envID string) string {
	mac := hmac.New(sha256.New, []byte(p.opts.BranchKey.Reveal()))
	for _, part := range []string{"prepared-v1", p.opts.Project, p.opts.SourceInstance, name, version, envID} {
		_, _ = mac.Write([]byte(part))
		_, _ = mac.Write([]byte{0})
	}
	return hex.EncodeToString(mac.Sum(nil))[:48]
}

func (p *Provider) requirePrepared(in *instance, version, envID string) error {
	if !p.owns(in) || in.Name != p.instanceName(branchPrefix, envID) ||
		in.Settings.UserLabels[envLabelKey] != envID ||
		in.Settings.UserLabels[fromLabelKey] != shortVersion(version) ||
		in.Settings.UserLabels[goldenLabelKey] != "" ||
		in.State != "RUNNABLE" || version == "" || envID == "" ||
		!hmac.Equal([]byte(in.Settings.UserLabels[preparedLabelKey]), []byte(p.preparedToken(in.Name, version, envID))) {
		return fmt.Errorf("cloudsql: instance %q has no completed preparation for this environment and golden; reconcile or remove the unfinished branch before retrying", in.Name)
	}
	return nil
}

func (p *Provider) sourceIdentity() string {
	sum := sha256.Sum256([]byte(p.opts.Project + "\x00" + p.opts.SourceInstance))
	return hex.EncodeToString(sum[:])[:32]
}

func (p *Provider) owns(in *instance) bool {
	return isOurs(in) && in.Settings.UserLabels[sourceLabelKey] == p.sourceIdentity()
}

// Admission is serialized across provider objects in this process. Independent
// engines still need a shared coordinator for a global branch quota.
var branchAdmissions sync.Map

func (p *Provider) admit(ctx context.Context) (func(), error) {
	key := p.api.endpoint + "\x00" + p.sourceIdentity()
	stored, _ := branchAdmissions.LoadOrStore(key, make(chan struct{}, 1))
	slot := stored.(chan struct{})
	select {
	case slot <- struct{}{}:
		return func() { <-slot }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *Provider) requireBranchIdentity(in *instance, b provider.Branch) error {
	if b.EnvID == "" {
		b.EnvID = in.Settings.UserLabels[envLabelKey]
	}
	if b.From == "" {
		var ok bool
		b.From, ok = decodeValue(in.Settings.UserLabels[versionLabelKey])
		if !ok {
			return fmt.Errorf("cloudsql: branch has no exact golden version")
		}
	}
	return p.requirePrepared(in, b.From, b.EnvID)
}
