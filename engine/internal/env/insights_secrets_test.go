package env

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The rehearsal runs the service's own migrate command, so it needs what the
// service is given.
//
// It was given the manifest's literals and nothing else. Every variable the
// service read from a secret source reached the rehearsal container as an
// empty string, so a Rails app with no SECRET_KEY_BASE refused to boot and the
// rehearsal failed on configuration before a single migration ran, while af up
// had started the same service with every one of those values.

func rehearsalOrchestrator(t *testing.T, m *schema.Manifest, values map[string]string) *Orchestrator {
	t.Helper()
	root := repoWith(t, map[string]string{
		"Gemfile":                  "gem 'rails'",
		"db/migrate/001_create.rb": "class Create < ActiveRecord::Migration; end",
	})
	o, err := New(Options{
		Root: root, Branch: "main", Clock: clock.New(), Manifest: m,
		Getenv: func(k string) string { return values[k] },
	})
	require.NoError(t, err)
	return o
}

func rehearsalApplier(t *testing.T, o *Orchestrator) *insights.ContainerApplier {
	t.Helper()
	set := insights.Discover(os.DirFS(o.opts.Root))
	require.Equal(t, insights.ToolRails, set.Tool)
	applier, why, err := o.applierFor(t.Context(), &session{}, set, provider.Branch{})
	require.NoError(t, err)
	require.Empty(t, why)
	c, ok := applier.(*insights.ContainerApplier)
	require.True(t, ok)
	return c
}

func railsService(name, migrate string, env ...schema.EnvVar) schema.Service {
	return schema.Service{
		Name: name, Migrate: migrate, Env: env,
		Build: &schema.Build{Strategy: schema.BuildImage, Image: "myapp:built"},
	}
}

func TestApplierFor_TheRehearsalReceivesTheSecretsTheServiceResolved(t *testing.T) {
	m := &schema.Manifest{
		Name: "app",
		Services: []schema.Service{railsService("web", "bin/rails db:migrate",
			schema.EnvVar{Name: "SECRET_KEY_BASE"},
			schema.EnvVar{Name: "RAILS_ENV", Value: "production"},
		)},
		Database: &schema.Database{Provider: schema.DBDocker},
	}
	o := rehearsalOrchestrator(t, m, map[string]string{"SECRET_KEY_BASE": "fixture-key-base"})

	c := rehearsalApplier(t, o)
	require.Equal(t, "fixture-key-base", c.Env["SECRET_KEY_BASE"].Reveal(),
		"the rehearsal was handed an empty SECRET_KEY_BASE, so the app cannot boot to migrate")
	require.Equal(t, "production", c.Env["RAILS_ENV"].Reveal())
}

func TestApplierFor_TheRehearsalReceivesItsOwnScopedValueAndNotAnotherServices(t *testing.T) {
	scoped := func(name string) schema.EnvVar {
		return schema.EnvVar{Name: name, Scope: schema.ScopeService}
	}
	m := &schema.Manifest{
		Name: "app",
		Services: []schema.Service{
			railsService("web", "bin/rails db:migrate", scoped("SECRET_KEY_BASE")),
			railsService("jobs", "", scoped("SECRET_KEY_BASE")),
		},
		Database: &schema.Database{Provider: schema.DBDocker},
	}
	o := rehearsalOrchestrator(t, m, map[string]string{
		"WEB__SECRET_KEY_BASE":  "fixture-web-key",
		"JOBS__SECRET_KEY_BASE": "fixture-jobs-key",
	})

	c := rehearsalApplier(t, o)
	require.Equal(t, "fixture-web-key", c.Env["SECRET_KEY_BASE"].Reveal(),
		"the migrating service's rehearsal did not receive its own scoped value")
}

func TestApplierFor_AServicesOwnDatabaseURLNeverReplacesTheBranch(t *testing.T) {
	// The resolved half of the precedence, end to end through the applier the
	// orchestrator builds. Scoped, because a service's own DATABASE_URL is the
	// case scope exists for, and it is the value most likely to be a real
	// database's address.
	m := &schema.Manifest{
		Name: "app",
		Services: []schema.Service{railsService("web", "bin/rails db:migrate",
			schema.EnvVar{Name: "DATABASE_URL", Scope: schema.ScopeService},
		)},
		Database: &schema.Database{Provider: schema.DBDocker},
	}
	const own = "postgres://web@production.invalid:5432/app"
	o := rehearsalOrchestrator(t, m, map[string]string{"WEB__DATABASE_URL": own})

	c := rehearsalApplier(t, o)
	require.Equal(t, own, c.Env["DATABASE_URL"].Reveal(), "the service's own value was not resolved")

	const branch = "postgres://rehearsal@af-rehearsal-db:5432/app"
	var urls []string
	for _, kv := range c.Environment(secrets.New(branch)) {
		if v, ok := strings.CutPrefix(kv, "DATABASE_URL="); ok {
			urls = append(urls, v)
		}
	}
	require.Equal(t, []string{branch}, urls,
		"the rehearsal container would reach the database the service's secret names")
}
