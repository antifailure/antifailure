package k8s

import (
	"testing"

	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The Job that brings a datastore to its declared stance, before anything
// talks to a cluster.
//
// Worth having separately from the conformance suite for the reason the rest
// of this file is: the conformance suite needs a cluster and these decisions
// are all made before one is contacted, so a change that puts two pods behind
// one name fails on a laptop rather than only where somebody had a cluster.

func stanceSpec() (provider.EnvSpec, provider.StanceJob) {
	spec := provider.EnvSpec{
		EnvID:                "e1",
		DatabaseURL:          secretValue("postgres://pooled/db"),
		MigrationDatabaseURL: secretValue("postgres://direct/db"),
		Services: []provider.ServiceSpec{{
			Name: "bus", Image: "confluentinc/cp-kafka:7.6.0", Port: 9092,
			Command: "/etc/confluent/docker/run",
		}},
	}
	return spec, provider.StanceJob{
		Store: "bus", Stance: "topics_only", Service: "bus", Command: "kafka-topics --create",
	}
}

func TestAStanceJobIsNamedAfterTheStore(t *testing.T) {
	r := &Runtime{prefix: DefaultNamespacePrefix}
	spec, sj := stanceSpec()
	job := r.stanceJob(spec, spec.Services[0], sj, "af-env-e1", "10.43.0.9")

	// The store, not the service whose image it runs in. A failure has to name
	// the store somebody looks at, and the fidelity report reads this name
	// back out of the journal to say whether this environment's own run did
	// what the stance asks.
	require.Equal(t, "bus-stance", job.Name)
	require.Equal(t, provider.StanceJobName("bus"), job.Name)
}

func TestAStanceJobsPodIsNotBehindTheStoresOwnName(t *testing.T) {
	r := &Runtime{prefix: DefaultNamespacePrefix}
	spec, sj := stanceSpec()
	job := r.stanceJob(spec, spec.Services[0], sj, "af-env-e1", "10.43.0.9")

	// The Service object's selector is how a name resolves inside the
	// namespace, and it selects on this label. A one shot pod carrying the
	// store's own label would put two pods behind one name for as long as it
	// runs, and a topic created against the one that is exiting is a topic
	// nobody has.
	require.Equal(t, "bus-stance", job.Spec.Template.Labels[LabelService])
	require.NotEqual(t, "bus", job.Spec.Template.Labels[LabelService])

	selector := serviceObject("e1", "af-env-e1", spec.Services[0]).Spec.Selector
	require.NotEqual(t, selector[LabelService], job.Spec.Template.Labels[LabelService],
		"the stance job's pod matches the store's own Service selector")
}

func TestAStanceJobRunsTheCommandAndNotTheStore(t *testing.T) {
	r := &Runtime{prefix: DefaultNamespacePrefix}
	spec, sj := stanceSpec()
	c := job(t, r.stanceJob(spec, spec.Services[0], sj, "af-env-e1", "10.43.0.9"))

	// The store's image, because the commands that create a topic ship in it,
	// and the stance's command rather than the store's own: a job that
	// inherited the service command would start a second broker and never
	// finish.
	require.Equal(t, "confluentinc/cp-kafka:7.6.0", c.Image)
	require.Equal(t, []string{"/bin/sh", "-c", "kafka-topics --create"}, c.Command)
}

func TestAStanceJobDeclaresNoPortAndIsNeverProbed(t *testing.T) {
	r := &Runtime{prefix: DefaultNamespacePrefix}
	spec, sj := stanceSpec()
	c := job(t, r.stanceJob(spec, spec.Services[0], sj, "af-env-e1", "10.43.0.9"))

	// A Job pod answers on no port, and a readiness probe on a pod that is
	// meant to exit makes a successful run look unhealthy for as long as it
	// lives.
	require.Empty(t, c.Ports)
	require.Nil(t, c.ReadinessProbe)
}

func TestAStanceJobGetsTheOrdinaryDatabaseURL(t *testing.T) {
	r := &Runtime{prefix: DefaultNamespacePrefix}
	spec, sj := stanceSpec()
	c := job(t, r.stanceJob(spec, spec.Services[0], sj, "af-env-e1", "10.43.0.9"))

	// The pooler bypass exists because a schema migration uses session level
	// features a transaction pooler does not support. A rebuild reads the
	// branch the way the application reads it, so handing it the direct
	// connection would be one job talking to the database through a door
	// nothing else in the environment uses.
	require.Equal(t, "postgres://pooled/db", envValue(t, c.Env, "DATABASE_URL"))
}

func TestAStanceJobIsNeverRetried(t *testing.T) {
	r := &Runtime{prefix: DefaultNamespacePrefix}
	spec, sj := stanceSpec()
	j := r.stanceJob(spec, spec.Services[0], sj, "af-env-e1", "10.43.0.9")

	// One clear failure beats six minutes of a Job that is neither running nor
	// finished, and the environment is failed either way.
	require.NotNil(t, j.Spec.BackoffLimit)
	require.Equal(t, int32(0), *j.Spec.BackoffLimit)
	require.Equal(t, corev1.RestartPolicyNever, j.Spec.Template.Spec.RestartPolicy)
}

func TestTheMigrationJobStillGetsTheDirectConnection(t *testing.T) {
	// The control on the assertion above. Both jobs go through one builder
	// now, so a change that gave the stance job the migration URL by making
	// them identical has to be visible as this test going red too.
	r := &Runtime{prefix: DefaultNamespacePrefix}
	spec, _ := stanceSpec()
	spec.Services[0].Migrate = "migrate"
	c := job(t, r.migrationJob(spec, spec.Services[0], "af-env-e1", "10.43.0.9"))
	require.Equal(t, "postgres://direct/db", envValue(t, c.Env, "DATABASE_URL"))
	require.Equal(t, []string{"/bin/sh", "-c", "migrate"}, c.Command)
}

// job returns the one container a one shot Job runs, and fails rather than
// panicking when a builder stops putting one there.
func job(t *testing.T, j *batchv1.Job) corev1.Container {
	t.Helper()
	require.Len(t, j.Spec.Template.Spec.Containers, 1,
		"a one shot job runs exactly one container")
	return j.Spec.Template.Spec.Containers[0]
}
