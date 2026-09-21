package iac

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func readTree(t *testing.T, files map[string]string, opts ...Option) *Reading {
	t.Helper()
	r, err := Read(context.Background(), writeTree(t, files), opts...)
	require.NoError(t, err)
	return r
}

func componentAt(t *testing.T, r *Reading, address string) Component {
	t.Helper()
	for _, c := range r.Components {
		if c.Address == address {
			return c
		}
	}
	var have []string
	for _, c := range r.Components {
		have = append(have, c.Address)
	}
	t.Fatalf("no component %s; the reading has %v", address, have)
	return Component{}
}

// TestAValueThatCouldNotBeReadIsNotZero is the property the whole package is
// arranged around, so it is asserted on all three answers at once.
//
// The middle case is the one that matters. A variable with no default is a
// value this reader cannot resolve without running Terraform, and the failure
// this test exists to prevent is reporting it as 0, or as "", which a consumer
// comparing production against a twin would read as agreement.
func TestAValueThatCouldNotBeReadIsNotZero(t *testing.T) {
	t.Parallel()
	r := readTree(t, map[string]string{"main.tf": `
variable "replicas" {
  type = number
}
variable "known_size" {
  type    = string
  default = "GP_Standard_D2s_v3"
}
resource "azurerm_postgresql_flexible_server" "main" {
  name      = "db"
  version   = "16"
  sku_name  = var.known_size
  zone      = var.replicas
}
`})
	c := componentAt(t, r, "azurerm_postgresql_flexible_server.main")

	// Known: read from the configuration.
	v, ok := c.Version.Get()
	require.True(t, ok)
	require.Equal(t, "16", v)
	require.Equal(t, Known, c.Version.State())

	// Absent: the configuration does not declare it. The zero value of Value
	// is this, which is why a reader that forgets a field says "not declared"
	// rather than "declared as nothing".
	require.Equal(t, Absent, c.CPU.State())
	_, ok = c.CPU.Get()
	require.False(t, ok)
	require.Empty(t, c.CPU.Why())

	// Unreadable: declared, and not resolvable here. It must carry the reason
	// and the line, and Get must refuse it exactly as Absent does.
	var zone Attr
	for _, a := range c.Attrs {
		if a.Name == "zone" {
			zone = a
		}
	}
	require.Equal(t, Unreadable, zone.Value.State(), "a variable with no default read as %s", zone.Value.State())
	_, ok = zone.Value.Get()
	require.False(t, ok, "Get returned a value for something that could not be read")
	require.Contains(t, zone.Value.Why(), "variable replicas has no default")
	require.Equal(t, "main.tf", zone.Value.At().File)
	require.Positive(t, zone.Value.At().Line, "an unreadable value with no line is not actionable")

	// A resolvable variable is resolved, or the unreadable answers above would
	// merely be this reader failing at everything.
	var sku Attr
	for _, a := range c.Attrs {
		if a.Name == "sku_name" {
			sku = a
		}
	}
	got, ok := sku.Value.Get()
	require.True(t, ok, "a variable WITH a default was not resolved, so the test above proves nothing")
	require.Equal(t, "GP_Standard_D2s_v3", got)
}

// TestUnmeasuredCollectsBothLevels proves the one call a consumer needs.
func TestUnmeasuredCollectsBothLevels(t *testing.T) {
	t.Parallel()
	r := readTree(t, map[string]string{
		"main.tf": `
variable "tag" {}
resource "azurerm_container_app" "api" {
  name = "api"
  template {
    container {
      name  = "api"
      image = var.tag
    }
  }
}
`,
		"chart/Chart.yaml": "name: api\nversion: 1.0.0\n",
	})
	var fieldLevel, fileLevel bool
	for _, u := range r.Unmeasured() {
		if strings.Contains(u.What, "image") && strings.Contains(u.Why, "variable tag has no default") {
			fieldLevel = true
		}
		if strings.Contains(u.What, "chart/Chart.yaml") && strings.Contains(u.Why, "Helm") {
			fileLevel = true
		}
	}
	require.True(t, fieldLevel, "Unmeasured did not carry the field this reader could not resolve")
	require.True(t, fileLevel, "Unmeasured did not carry the file this reader did not read")
}

// TestASensitiveVariableIsNeverCarried covers the control that is enforced at
// resolution time rather than filtered afterwards.
func TestASensitiveVariableIsNeverCarried(t *testing.T) {
	t.Parallel()
	const marker = "FIXTURE-VALUE-NOT-A-CREDENTIAL"
	r := readTree(t, map[string]string{"main.tf": `
variable "marked" {
  type      = string
  sensitive = true
  default   = "` + marker + `"
}
variable "plain" {
  type    = string
  default = "ordinary"
}
resource "aws_s3_bucket" "assets" {
  bucket      = var.plain
  description = var.marked
}
`})
	require.NotContains(t, dump(t, r), marker,
		"a variable the configuration itself marked sensitive reached the reading")

	c := componentAt(t, r, "aws_s3_bucket.assets")
	var desc, bucket Attr
	for _, a := range c.Attrs {
		switch a.Name {
		case "description":
			desc = a
		case "bucket":
			bucket = a
		}
	}
	require.Equal(t, Unreadable, desc.Value.State())
	require.Contains(t, desc.Value.Why(), "declared sensitive")
	// The control arm: an ordinary variable is still resolved, so the result
	// above is the sensitive flag working rather than variables not working.
	got, ok := bucket.Value.Get()
	require.True(t, ok)
	require.Equal(t, "ordinary", got)
}

// TestACredentialNamedAttributeIsDropped covers the name based control, in
// both directions: it has to drop a credential AND let a locator through,
// because a filter that drops everything is as useless as one that drops
// nothing.
func TestACredentialNamedAttributeIsDropped(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		dropped bool
	}{
		{"administrator_login_password", true},
		{"connection_string", true},
		{"secret_access_key", true},
		{"client_secret", true},
		{"sas_token", true},
		{"private_key", true},
		// Locators. Every one of these is something a report NEEDS.
		{"key_vault_secret_id", false},
		{"key_vault_id", false},
		{"access_key_id", false},
		{"secret_name", false},
		{"secret_version", false},
		{"partition_key", false},
		{"administrator_login", false},
		{"name", false},
		{"sku_name", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equalf(t, tc.dropped, credentialName(tc.name),
				"credentialName(%q) = %v", tc.name, !tc.dropped)
		})
	}
}

// TestACredentialNamedAttributeIsDroppedFromARealReading is the end to end arm
// of the table above, and it exists because the table above passed while the
// control was disconnected.
//
// The mutation table found it: breaking the credentialName branch inside scrub
// left every test green, because the only test of credentialName called the
// PREDICATE directly. A predicate with a correct answer and no reachable call
// site is a dead control that looks like a working one, which is the exact
// failure the "verify it works, do not verify that it exists" rule names.
func TestACredentialNamedAttributeIsDroppedFromARealReading(t *testing.T) {
	t.Parallel()
	const marker = "FIXTURE-VALUE-NOT-A-CREDENTIAL"
	r := readTree(t, map[string]string{"main.tf": `
resource "azurerm_postgresql_flexible_server" "db" {
  name                   = "db"
  administrator_login    = "af_migrator"
  administrator_password = "` + marker + `"
}
`})
	require.NotContains(t, dump(t, r), marker,
		"an attribute whose NAME says it holds a credential reached the reading")
	require.Equal(t, Withheld, attrOf(t, r, "azurerm_postgresql_flexible_server.db", "administrator_password").State(),
		"a credential must be WITHHELD, not merely unreadable: a consumer counting holes in the "+
			"description of production must not count a password this reader deliberately refused")
	require.Contains(t, attrOf(t, r, "azurerm_postgresql_flexible_server.db", "administrator_password").Why(),
		"name says it holds a credential")
	// The control arm: the LOGIN is a locator and must survive, or the rule is
	// dropping the resource rather than the credential.
	require.Equal(t, "af_migrator",
		attrOf(t, r, "azurerm_postgresql_flexible_server.db", "administrator_login").Or(""))
}

// TestAResourceTypeThatHoldsASecretHasItDropped covers the control no name
// based rule can have: the attribute is called `value`.
func TestAResourceTypeThatHoldsASecretHasItDropped(t *testing.T) {
	t.Parallel()
	const marker = "FIXTURE-VALUE-NOT-A-CREDENTIAL"
	r := readTree(t, map[string]string{"main.tf": `
resource "azurerm_key_vault_secret" "session" {
  name         = "session-key"
  value        = "` + marker + `"
  content_type = "text/plain"
}
`})
	require.NotContains(t, dump(t, r), marker,
		"a key vault secret's value reached the reading, which no name based rule would catch")

	c := componentAt(t, r, "azurerm_key_vault_secret.session")
	require.Equal(t, KindSecret, c.Kind)
	require.Equal(t, "session-key", c.Name, "the secret's NAME is a reference and must survive")
	var value, contentType Attr
	for _, a := range c.Attrs {
		switch a.Name {
		case "value":
			value = a
		case "content_type":
			contentType = a
		}
	}
	require.Equal(t, Withheld, value.Value.State())
	require.Contains(t, value.Value.Why(), "holds a secret in this attribute")
	// The control arm: an innocuous attribute of the same resource survives.
	got, ok := contentType.Value.Get()
	require.True(t, ok, "the type based rule dropped the whole resource rather than one attribute")
	require.Equal(t, "text/plain", got)
}

// TestThePlanSensitiveMaskIsHonoured is run against a plan Terraform itself
// produced, because the whole point is what Terraform actually writes.
//
// MEASURED, not assumed: terraform v1.15.8 puts a sensitive variable's value
// in the clear in `planned_values...values`, in `resource_changes[].change.after`
// and in the top level `variables` object, and marks it in only the first two.
func TestThePlanSensitiveMaskIsHonoured(t *testing.T) {
	t.Parallel()
	plan := string(goldenBytes(t, "terraform-plan.golden"))
	const marker = "FIXTURE-VALUE-NOT-A-CREDENTIAL"

	// The fixture must be one worth testing. If Terraform ever stops writing
	// the value in the clear, this assertion fails and tells the next person
	// that the danger changed rather than letting the test quietly become a
	// no-op.
	require.Contains(t, plan, marker,
		"the plan fixture no longer carries a sensitive value in the clear, so this test proves nothing")
	require.Contains(t, plan, `"sensitive_values"`, "the plan fixture carries no sensitivity mask")

	r := readTree(t, map[string]string{"plan.json": plan})
	require.NotContains(t, dump(t, r), marker, "a value the plan marked sensitive was published")

	// The control arm: the unmasked resource in the same plan IS read, so the
	// clean result above is the mask working rather than the plan reader
	// failing to read anything.
	plain := componentAt(t, r, "terraform_data.plain")
	var input Attr
	for _, a := range plain.Attrs {
		if a.Name == "input" {
			input = a
		}
	}
	got, ok := input.Value.Get()
	require.True(t, ok, "the plan reader read nothing at all, so the mask proves nothing")
	require.Equal(t, "small", got)

	masked := componentAt(t, r, "terraform_data.thing")
	for _, a := range masked.Attrs {
		if a.Name == "input" {
			require.Equal(t, Withheld, a.Value.State())
			require.Contains(t, a.Value.Why(), "marks this value sensitive")
		}
	}
}

// TestAMaskThatWillNotDecodeMasksEverything proves the direction the mask
// fails in. "I cannot tell whether this is a secret" must never publish.
func TestAMaskThatWillNotDecodeMasksEverything(t *testing.T) {
	t.Parallel()
	// Three shapes reach three different branches, and the middle one is the
	// one the mutation table found uncovered: a mask that STARTS as an object
	// and does not decode takes a different path from a mask that is not an
	// object at all, and only the second was being tested.
	for _, undecodable := range []string{`"this is not a mask"`, `{"a":`, `[true,`} {
		bad := newMask(json.RawMessage(undecodable))
		require.Truef(t, bad.all, "the undecodable mask %s did not mask", undecodable)
	}
	m := newMask(json.RawMessage(`"this is not a mask"`))
	require.True(t, m.all, "an undecodable sensitivity mask did not mask")
	require.True(t, m.child("anything").all)
	require.True(t, m.index(3).all)
	// And the arm that must NOT mask, or everything would be unreadable and
	// the reader would be useless rather than safe.
	require.False(t, newMask(json.RawMessage(`{}`)).all)
	require.False(t, newMask(nil).all)
	require.False(t, newMask(json.RawMessage(`{"a":true}`)).child("b").all)
	require.True(t, newMask(json.RawMessage(`{"a":true}`)).child("a").all)
}

// TestADynamicBlockIsDeclaredWithoutBeingCertain covers the third answer a
// reader of real configurations needs constantly.
func TestADynamicBlockIsDeclaredWithoutBeingCertain(t *testing.T) {
	t.Parallel()
	r := readTree(t, map[string]string{"main.tf": `
variable "analytics_enabled" {
  type = bool
}
resource "azurerm_container_app" "api" {
  name = "api"
  template {
    container {
      name  = "api"
      image = "ghcr.io/antifailure/api:1.0.0"
      env {
        name  = "AF_PORT"
        value = "8080"
      }
      dynamic "env" {
        for_each = var.analytics_enabled ? [1] : []
        content {
          name  = "AF_ANALYTICS"
          value = "1"
        }
      }
    }
  }
}
`})
	c := componentAt(t, r, "azurerm_container_app.api")
	byName := map[string]EnvVar{}
	for _, e := range c.Env {
		byName[e.Name] = e
	}
	require.Contains(t, byName, "AF_PORT")
	require.Contains(t, byName, "AF_ANALYTICS",
		"a variable inside a dynamic block was dropped, so the environment is understated")

	certain, ok := byName["AF_PORT"].Present.Get()
	require.True(t, ok)
	require.True(t, certain)

	_, ok = byName["AF_ANALYTICS"].Present.Get()
	require.False(t, ok, "a conditionally declared variable was reported as certainly present")
	require.Equal(t, Unreadable, byName["AF_ANALYTICS"].Present.State())
	require.Contains(t, byName["AF_ANALYTICS"].Present.Why(), "dynamic block")

	// The image is not a variable, so it must be plainly Known: this is the
	// arm that would catch a reader which marked everything uncertain.
	img, ok := c.Image.Get()
	require.True(t, ok)
	require.Equal(t, "ghcr.io/antifailure/api:1.0.0", img)
}

// TestACountThatCannotBeResolvedIsNotAbsence covers the same third answer at
// the level of a whole resource.
func TestACountThatCannotBeResolvedIsNotAbsence(t *testing.T) {
	t.Parallel()
	r := readTree(t, map[string]string{"main.tf": `
variable "portal_enabled" {
  type = bool
}
resource "aws_s3_bucket" "maybe" {
  count  = var.portal_enabled ? 1 : 0
  bucket = "maybe"
}
resource "aws_s3_bucket" "never" {
  count  = 0
  bucket = "never"
}
resource "aws_s3_bucket" "always" {
  bucket = "always"
}
`})
	maybe := componentAt(t, r, "aws_s3_bucket.maybe")
	require.Equal(t, Unreadable, maybe.Present.State(),
		"a resource behind an unresolvable count was reported as definitely present or absent")
	require.Contains(t, maybe.Present.Why(), "count")

	never, ok := componentAt(t, r, "aws_s3_bucket.never").Present.Get()
	require.True(t, ok)
	require.False(t, never, "count = 0 is a decision, not an unknown")

	always, ok := componentAt(t, r, "aws_s3_bucket.always").Present.Get()
	require.True(t, ok)
	require.True(t, always)
}

// TestOneBadDocumentDoesNotBlankTheFile is the decode boundary rule: a stream
// of external documents must not be all or nothing.
func TestOneBadDocumentDoesNotBlankTheFile(t *testing.T) {
	t.Parallel()
	good := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
spec:
  replicas: 3
  template:
    spec:
      containers:
        - name: api
          image: ghcr.io/antifailure/api:1.0.0
`
	r := readTree(t, map[string]string{"all.yaml": good + "---\nkind: Deployment\n  bad: [indent\n"})

	c := componentAt(t, r, "Deployment default/api")
	n, ok := c.Replicas.Get()
	require.True(t, ok, "a malformed document later in the file blanked the document before it")
	require.Equal(t, 3, n)

	var src Source
	for _, s := range r.Sources {
		if s.Path == "all.yaml" {
			src = s
		}
	}
	require.NotEmpty(t, src.Why, "the file was read with a broken document in it and said nothing")
	require.Contains(t, src.Why, "did not parse")
}

// TestAKubernetesWorkloadIsReadWithItsShape covers the raw manifest reader.
func TestAKubernetesWorkloadIsReadWithItsShape(t *testing.T) {
	t.Parallel()
	r := readTree(t, map[string]string{"api.yaml": `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
  namespace: prod
spec:
  replicas: 4
  template:
    spec:
      containers:
        - name: api
          image: ghcr.io/antifailure/api:2.1.0
          resources:
            limits:
              cpu: "500m"
              memory: "512Mi"
          env:
            - name: AF_PORT
              value: "8080"
            - name: AF_DATABASE_URL
              valueFrom:
                secretKeyRef:
                  name: db
                  key: url
          envFrom:
            - secretRef:
                name: extras
          livenessProbe:
            httpGet:
              path: /healthz
              port: 8080
            timeoutSeconds: 3
            periodSeconds: 10
`})
	c := componentAt(t, r, "Deployment prod/api")
	require.Equal(t, KindService, c.Kind)
	require.Equal(t, 4, c.Replicas.Or(0))
	require.Equal(t, "ghcr.io/antifailure/api:2.1.0", c.Image.Or(""))
	require.Equal(t, "500m", c.CPU.Or(""))
	require.Equal(t, "512Mi", c.Memory.Or(""))

	byName := map[string]EnvVar{}
	for _, e := range c.Env {
		byName[e.Name] = e
	}
	require.Contains(t, byName, "AF_PORT")
	require.Equal(t, "db/url", byName["AF_DATABASE_URL"].From.Or(""),
		"a secretKeyRef is a reference and must be carried")

	// envFrom imports names this file does not contain, and that has to be
	// unreadable rather than silently nothing.
	var envFrom EnvVar
	for _, e := range c.Env {
		if strings.Contains(e.Name, "every key of") {
			envFrom = e
		}
	}
	require.NotEmpty(t, envFrom.Name, "envFrom was dropped, so the environment is understated")
	require.Equal(t, Unreadable, envFrom.Present.State())

	require.Len(t, c.Probes, 1)
	require.Equal(t, "liveness", c.Probes[0].Type)
	require.Equal(t, "/healthz", c.Probes[0].Path.Or(""))
	require.Equal(t, 3, c.Probes[0].TimeoutSeconds.Or(0))
	require.Equal(t, 10, c.Probes[0].PeriodSeconds.Or(0))
	require.Positive(t, c.Probes[0].At.Line)
}

// TestAKubernetesSecretIsNamedAndNotRead covers a Secret's data.
func TestAKubernetesSecretIsNamedAndNotRead(t *testing.T) {
	t.Parallel()
	const marker = "RkVYVFVSRS1OT1QtQS1DUkVERU5USUFM"
	r := readTree(t, map[string]string{"s.yaml": `
apiVersion: v1
kind: Secret
metadata:
  name: db
data:
  url: ` + marker + `
`})
	require.NotContains(t, dump(t, r), marker, "a Kubernetes secret's data reached the reading")
	c := componentAt(t, r, "Secret default/db")
	require.Equal(t, KindSecret, c.Kind)
	require.Len(t, c.Attrs, 1)
	require.Equal(t, "data.url", c.Attrs[0].Name, "the KEY is a reference and must survive")
	require.Equal(t, Withheld, c.Attrs[0].Value.State())
}

// TestANetworkPolicyEmptyEgressIsTheStrongestStatement covers the case where
// absence is the fact.
func TestANetworkPolicyEmptyEgressIsTheStrongestStatement(t *testing.T) {
	t.Parallel()
	r := readTree(t, map[string]string{"np.yaml": `
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: lockdown
spec:
  policyTypes: ["Egress"]
  egress: []
`})
	require.Len(t, r.Network, 1, "an empty egress list produced no rule, so a policy that denies "+
		"all egress reads exactly like a tree with no policy in it")
	require.Equal(t, Egress, r.Network[0].Direction)
	require.False(t, r.Network[0].Allow)
	require.Contains(t, r.Network[0].To.Or(""), "denies all of it")
}

// TestAKustomizeImageOverrideIsApplied covers the silent wrongness a
// kustomization creates.
func TestAKustomizeImageOverrideIsApplied(t *testing.T) {
	t.Parallel()
	r := readTree(t, map[string]string{
		"base/api.yaml": `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
spec:
  replicas: 1
  template:
    spec:
      containers:
        - name: api
          image: ghcr.io/antifailure/api:1.0.0
`,
		"overlay/kustomization.yaml": `
resources:
  - ../base/api.yaml
images:
  - name: ghcr.io/antifailure/api
    newTag: 9.9.9
replicas:
  - name: api
    count: 6
`,
	})
	c := componentAt(t, r, "Deployment default/api")
	require.Equal(t, "ghcr.io/antifailure/api:9.9.9", c.Image.Or(""),
		"the overlay's image override was not applied, so this reading describes a tag production "+
			"does not run")
	require.Equal(t, 6, c.Replicas.Or(0))
}

// TestAKustomizeTransformerThatIsNotAppliedSaysSo covers the other half: what
// this reader will not do has to be visible.
func TestAKustomizeTransformerThatIsNotAppliedSaysSo(t *testing.T) {
	t.Parallel()
	r := readTree(t, map[string]string{
		"base/api.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: api\nspec:\n  replicas: 1\n",
		"overlay/kustomization.yaml": `
resources:
  - ../base/api.yaml
patches:
  - path: bump.yaml
    target:
      kind: Deployment
      name: api
`,
	})
	var src Source
	for _, s := range r.Sources {
		if s.Path == "overlay/kustomization.yaml" {
			src = s
		}
	}
	require.False(t, src.Read, "an overlay with a patch in it reported itself fully read")
	require.Contains(t, src.Why, "a patch targeting Deployment api")
	require.Contains(t, src.Why, "does not apply")
}

// TestTheKindVocabularyMatchesDetect is the promise the Kind doc comment
// makes. It is a real comparison against the other package's table rather than
// a list copied twice, because two copies is exactly how they drift.
func TestTheKindVocabularyMatchesDetect(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile(filepath.Join("..", "detect", "container.go"))
	require.NoError(t, err, "engine/internal/detect moved, and this test can no longer check that "+
		"the two vocabularies agree")
	src := string(body)
	start := strings.Index(src, "infraImages = map[string]string{")
	require.Positive(t, start, "detect's infraImages table was renamed, so this test could not look")
	end := strings.Index(src[start:], "\n}")
	require.Positive(t, end)
	table := src[start : start+end]

	shared := []Kind{
		KindPostgres, KindRedis, KindMySQL, KindMongoDB, KindRabbitMQ,
		KindElasticsearch, KindClickHouse, KindKafka, KindObjectStore,
	}
	for _, k := range shared {
		require.Containsf(t, table, `"`+string(k)+`"`,
			"this package calls something %q and engine/internal/detect has no such word, so a "+
				"fidelity report comparing the two would silently stop matching", k)
	}
	// The falsification arm: a word detect does NOT have must not be found, or
	// the search above would pass for anything.
	require.NotContains(t, table, `"bucket"`,
		"the instrument finds words that are not in the table, so its agreement above says nothing")
}

// TestWithheldIsNotTheSameAnswerAsUnresolved covers the fourth state.
//
// It exists because a test forced it. Asserting that a plan produces nothing
// unresolvable failed, correctly, on a value the plan marked sensitive: the
// reader had resolved that one perfectly well and was refusing to carry it,
// which is the opposite of a hole. Collapsing the two made the plan path, whose
// whole selling point is that nothing is unresolvable, look exactly as ragged
// as the HCL path.
func TestWithheldIsNotTheSameAnswerAsUnresolved(t *testing.T) {
	t.Parallel()
	r := readTree(t, map[string]string{"main.tf": `
variable "unset" {
  type = string
}
resource "azurerm_postgresql_flexible_server" "db" {
  name                   = "db"
  zone                   = var.unset
  administrator_password = "FIXTURE-VALUE-NOT-A-CREDENTIAL"
}
`})
	hole := attrOf(t, r, "azurerm_postgresql_flexible_server.db", "zone")
	secret := attrOf(t, r, "azurerm_postgresql_flexible_server.db", "administrator_password")

	require.Equal(t, Unreadable, hole.State(), "a value this reader could not work out")
	require.Equal(t, Withheld, secret.State(), "a value this reader worked out and will not carry")

	// Both refuse Get, so a consumer cannot read a credential by failing to
	// notice that a new state exists.
	_, ok := secret.Get()
	require.False(t, ok, "Get returned a withheld credential")
	_, ok = hole.Get()
	require.False(t, ok)

	// Both appear in Unmeasured, because a caller rendering "what I cannot
	// tell you" wants both, and they are marked apart.
	var sawHole, sawSecret bool
	for _, u := range r.Unmeasured() {
		if strings.Contains(u.What, "zone") {
			sawHole = true
			require.False(t, u.Withheld, "a hole was marked as a withheld credential")
		}
		if strings.Contains(u.What, "administrator_password") {
			sawSecret = true
			require.True(t, u.Withheld, "a withheld credential was not marked as one")
		}
	}
	require.True(t, sawHole && sawSecret, "Unmeasured lost one of the two")

	// Unresolved carries ONLY the hole. This is the call a consumer counting
	// defects uses, and counting a refused password as a defect would send
	// somebody hunting for a bug that is not there.
	for _, u := range r.Unresolved() {
		require.NotContainsf(t, u.What, "administrator_password",
			"Unresolved counted a credential this reader deliberately refused as a hole")
	}
	require.NotEmpty(t, r.Unresolved(), "Unresolved dropped the genuine hole as well")
}

// TestOrTreatsEveryNonKnownStateAlike covers the two rules team-lead set for
// Or: a consumer wanting the distinction has Get and State and must be made to
// use them, and Or is for proceeding, never for concluding.
func TestOrTreatsEveryNonKnownStateAlike(t *testing.T) {
	t.Parallel()
	at := Position{File: "main.tf", Line: 1}
	require.Equal(t, 3, Value[int]{}.Or(3), "Absent")
	require.Equal(t, 3, Unresolved[int]("could not work it out", at).Or(3), "Unreadable")
	require.Equal(t, 3, Refused[int]("it is a credential", at).Or(3), "Withheld")
	require.Equal(t, 7, Resolved(7, at).Or(3), "Known must return the value, not the fallback")

	// The three non-Known states must be INDISTINGUISHABLE through Or, so a
	// caller who wants to tell them apart is forced to State or Get.
	require.Equal(t,
		[]int{Value[int]{}.Or(3), Unresolved[int]("a", at).Or(3), Refused[int]("b", at).Or(3)},
		[]int{3, 3, 3})
}

// TestAFileTooLargeToReadSaysSoRatherThanSkipping covers the size ceiling.
//
// It exists because golangci-lint's `unused` check caught withMaxBytes with no
// callers, on CI, after the package was otherwise finished. The option had
// been written FOR this test and the test was never written, which left a real
// behaviour with no proof: a file above the ceiling is not read, and the
// difference between saying so and skipping quietly is the difference between
// a description of production with a stated hole in it and one that is wrong.
func TestAFileTooLargeToReadSaysSoRatherThanSkipping(t *testing.T) {
	t.Parallel()
	big := "# " + strings.Repeat("padding ", 200) + "\n" +
		"resource \"aws_s3_bucket\" \"huge\" {\n  bucket = \"huge\"\n}\n"
	r := readTree(t, map[string]string{
		"big.tf":   big,
		"small.tf": "resource \"aws_sqs_queue\" \"jobs\" {\n  name = \"jobs\"\n}\n",
	}, withMaxBytes(256))

	var bigSrc Source
	for _, s := range r.Sources {
		if s.Path == "big.tf" {
			bigSrc = s
		}
	}
	require.False(t, bigSrc.Read, "a file above the ceiling was read anyway")
	require.Contains(t, bigSrc.Why, "ceiling",
		"the file was skipped without saying why, so its resources are missing and nothing says so")
	require.Contains(t, bigSrc.Why, "256")

	// It is a HOLE, which means it has to reach the caller through the one
	// call that collects them.
	require.Contains(t, renderUnmeasuredList(r.Unmeasured()), "big.tf")

	// The control arm: the file beside it is still read, and the same tree
	// read WITHOUT the ceiling reads both, so the result above is the ceiling
	// working rather than the reader failing.
	require.Equal(t, "aws_sqs_queue.jobs", componentAt(t, r, "aws_sqs_queue.jobs").Address)
	for _, c := range r.Components {
		require.NotEqual(t, "aws_s3_bucket.huge", c.Address)
	}
	full := readTree(t, map[string]string{"big.tf": big})
	require.Equal(t, "aws_s3_bucket.huge", componentAt(t, full, "aws_s3_bucket.huge").Address,
		"the file is unreadable even without a ceiling, so the ceiling proves nothing")
}

func renderUnmeasuredList(us []Unmeasured) string {
	var sb strings.Builder
	for _, u := range us {
		sb.WriteString(u.String())
		sb.WriteString("\n")
	}
	return sb.String()
}
