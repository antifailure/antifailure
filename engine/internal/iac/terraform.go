package iac

import (
	"sort"
	"strings"
)

// readTerraform reads one root module: every .tf and .tfvars file in a single
// directory, evaluated together.
//
// A DIRECTORY AT A TIME, because that is what a root module is. Variables are
// declared in variables.tf and referenced from database.tf, locals in one file
// feed resources in another, and a reader handed one file at a time would
// report as unreadable every value the file beside it defines. That would be
// an honest answer to the wrong question.
func readTerraform(cfg *config, files []*candidate, out *Reading) {
	type parsed struct {
		c    *candidate
		body hclBody
	}
	var bodies []parsed
	for _, c := range files {
		if c.dialect == DialectTerraformJSON {
			// Terraform's JSON syntax is a different grammar with the same
			// semantics. It is named rather than read, because reading it
			// properly means a second evaluator and a half read one would be
			// the silent wrongness this package is built to avoid.
			out.Sources = append(out.Sources, Source{
				Path: c.rel, Dialect: c.dialect, Read: false,
				Why: "this is Terraform's JSON syntax, which this reader recognises and does " +
					"not yet read; its resources are missing from this description",
			})
			continue
		}
		toks, lerr := lex(c.rel, c.body)
		if lerr != nil {
			out.Sources = append(out.Sources, Source{
				Path: c.rel, Dialect: c.dialect, Read: false, Why: lerr.why,
			})
			continue
		}
		body, perr := parseFile(toks)
		if perr != nil {
			out.Sources = append(out.Sources, Source{
				Path: c.rel, Dialect: c.dialect, Read: false, Why: perr.why,
			})
			continue
		}
		bodies = append(bodies, parsed{c: c, body: body})
	}
	if len(bodies) == 0 {
		return
	}

	ctx := &evalCtx{
		vars:      map[string]cval{},
		varDecl:   map[string]bool{},
		sensitive: map[string]bool{},
		locals:    map[string]hclAttr{},
		resolving: map[string]bool{},
	}
	if cfg.workspace != "" {
		ctx.workspace = Resolved(cfg.workspace, Position{})
	}

	// Pass one over the module: declarations, before anything referring to
	// them is evaluated.
	varFiles := map[string]bool{}
	for _, p := range cfg.varFiles {
		varFiles[strings.TrimPrefix(p, "./")] = true
	}
	for _, p := range bodies {
		if strings.HasSuffix(p.c.rel, ".tfvars") {
			continue
		}
		for _, blk := range p.body.blocksOf("variable") {
			if len(blk.labels) != 1 {
				continue
			}
			name := blk.labels[0]
			ctx.varDecl[name] = true
			if s, ok := blk.body.attr("sensitive"); ok {
				if v := ctx.eval(s.expr); v.st == Known && v.s == "true" {
					ctx.sensitive[name] = true
				}
			}
		}
		for _, blk := range p.body.blocksOf("locals") {
			for _, a := range blk.body.attrs {
				ctx.locals[a.name] = a
			}
		}
	}
	// Defaults, after the sensitive flags are known, so a sensitive
	// variable's default is never evaluated into a carried value.
	for _, p := range bodies {
		if strings.HasSuffix(p.c.rel, ".tfvars") {
			continue
		}
		for _, blk := range p.body.blocksOf("variable") {
			if len(blk.labels) != 1 || ctx.sensitive[blk.labels[0]] {
				continue
			}
			if d, ok := blk.body.attr("default"); ok {
				ctx.vars[blk.labels[0]] = ctx.eval(d.expr)
			}
		}
	}
	// A tfvars file the caller named wins over a default, which is Terraform's
	// own precedence. A tfvars file the caller did NOT name is ignored, and
	// its own Source records that, because a file the caller did not ask for
	// may belong to another environment entirely and reading it would
	// describe the wrong production.
	for _, p := range bodies {
		if !strings.HasSuffix(p.c.rel, ".tfvars") {
			continue
		}
		if !varFiles[p.c.rel] {
			out.Sources = append(out.Sources, Source{
				Path: p.c.rel, Dialect: p.c.dialect, Read: false,
				Why: "this variable file was not named for this read, and a variable file that " +
					"was not asked for may belong to a different environment, so its values " +
					"are not used",
			})
			continue
		}
		for _, a := range p.body.attrs {
			if ctx.sensitive[a.name] {
				continue
			}
			ctx.vars[a.name] = ctx.eval(a.expr)
		}
		out.Sources = append(out.Sources, Source{Path: p.c.rel, Dialect: p.c.dialect, Read: true})
	}

	// Pass two: the resources themselves.
	var pending []pendingParam
	for _, p := range bodies {
		if strings.HasSuffix(p.c.rel, ".tfvars") {
			continue
		}
		read := false
		for _, blk := range p.body.blocks {
			switch blk.typ {
			case "resource":
				if len(blk.labels) != 2 {
					continue
				}
				read = true
				if pp, ok := readServerParameter(ctx, p.c.rel, blk); ok {
					pending = append(pending, pp)
					continue
				}
				readResource(ctx, p.c.rel, blk, out)
			case "module":
				if len(blk.labels) != 1 {
					continue
				}
				read = true
				readModule(ctx, p.c.rel, blk, out)
			case "variable", "locals", "output", "terraform", "provider", "data", "moved", "import", "check":
				read = true
			}
		}
		out.Sources = append(out.Sources, Source{Path: p.c.rel, Dialect: p.c.dialect, Read: read})
	}

	attachServerParameters(pending, out)
}

// readServerParameter recognises a resource whose whole job is to set one
// server parameter, and reads it as a parameter rather than as a component.
//
// It returns false when the resource is one of those and its server cannot be
// identified, so that readResource records it as an ordinary component: a
// parameter attached to nothing is still a fact about production, and dropping
// it would be the silent loss this reader exists to avoid.
func readServerParameter(ctx *evalCtx, file string, blk hclBlock) (pendingParam, bool) {
	resType := blk.labels[0]
	if !serverParameterTypes[resType] {
		return pendingParam{}, false
	}
	target, ok := serverTargetOf(blk.body)
	if !ok {
		return pendingParam{}, false
	}
	at := Position{File: file, Line: blk.pos.Line, Col: blk.pos.Col}
	p := Param{Name: blk.labels[1], At: at}
	if a, ok := blk.body.attr("name"); ok {
		if v := ctx.eval(a.expr).str(); v.State() == Known {
			p.Name, _ = v.Get()
		}
	}
	if a, ok := blk.body.attr("value"); ok {
		p.Value = valueAt(ctx.eval(a.expr), Position{File: file, Line: a.pos.Line, Col: a.pos.Col})
	}
	return pendingParam{target: target, param: p}, true
}

// serverParameterTypes are the resource types that set ONE server parameter on
// a database that is declared somewhere else.
//
// They are the reason server parameters need a pass of their own. On Azure and
// on AWS a parameter is not an attribute of the server, it is a separate
// resource pointing back at it, so a reader that only looked inside the server
// block would report a Postgres with no parameters while production runs with
// half a dozen set.
var serverParameterTypes = map[string]bool{
	"azurerm_postgresql_flexible_server_configuration": true,
	"azurerm_postgresql_configuration":                 true,
	"azurerm_mysql_flexible_server_configuration":      true,
	"azurerm_mysql_configuration":                      true,
}

// pendingParam is a server parameter read from its own resource, waiting for
// the component it configures.
type pendingParam struct {
	target string
	param  Param
}

// serverTargetOf reads the address of the server a parameter resource points
// at.
//
// It reads the TRAVERSAL rather than the evaluated value, deliberately:
// `server_id = azurerm_postgresql_flexible_server.main.id` evaluates to
// unreadable by design, because an id only exists after an apply, and yet the
// first two elements of that reference are exactly the address needed here.
// The link between the two resources is in the configuration even though the
// value is not.
func serverTargetOf(body hclBody) (string, bool) {
	for _, name := range []string{"server_id", "server_name"} {
		a, ok := body.attr(name)
		if !ok {
			continue
		}
		parts, ok := traversalOf(a.expr)
		if !ok || len(parts) < 2 {
			continue
		}
		return parts[0] + "." + parts[1], true
	}
	return "", false
}

// attachServerParameters moves each parameter resource onto the component it
// configures.
//
// A parameter whose server reference cannot be read as an address stays where
// it is, as its own component, rather than being attached to a guess.
func attachServerParameters(pending []pendingParam, out *Reading) {
	if len(pending) == 0 {
		return
	}
	byAddress := map[string]int{}
	for i, c := range out.Components {
		byAddress[c.Address] = i
	}
	for _, p := range pending {
		idx, ok := byAddress[p.target]
		if !ok {
			continue
		}
		out.Components[idx].Params = append(out.Components[idx].Params, p.param)
	}
	for i := range out.Components {
		params := out.Components[i].Params
		sort.SliceStable(params, func(a, b int) bool { return params[a].Name < params[b].Name })
	}
}

// readModule records a module call as a source that could not be followed.
//
// A LOCAL module could in principle be read, and is not, deliberately: this
// reader is pointed at a root module directory and following a `source = "../
// modules/x"` would walk outside the tree it was given. A module whose source
// is a registry cannot be read at all without fetching it, which this package
// does not do. Both arrive as the same honest answer, with the source named so
// a person can see which case they have.
func readModule(ctx *evalCtx, file string, blk hclBlock, out *Reading) {
	src := "an unnamed source"
	if a, ok := blk.body.attr("source"); ok {
		if v := ctx.eval(a.expr); v.st == Known {
			src = v.s
		}
	}
	out.Sources = append(out.Sources, Source{
		Path:    file,
		Dialect: DialectTerraform,
		Read:    false,
		Why: "module." + blk.labels[0] + " comes from " + src + ", and this reader does not " +
			"follow a module, so whatever it declares is missing from this description",
	})
}

func readResource(ctx *evalCtx, file string, blk hclBlock, out *Reading) {
	resType, resName := blk.labels[0], blk.labels[1]
	address := resType + "." + resName
	at := Position{File: file, Line: blk.pos.Line, Col: blk.pos.Col}

	if rules, ok := networkRules(ctx, resType, address, at, blk); ok {
		out.Network = append(out.Network, rules...)
		return
	}

	c := Component{
		Name:    resName,
		Address: address,
		Type:    resType,
		At:      at,
		Kind:    KindUnknown,
		From:    DialectTerraform,
		Present: presenceOf(ctx, blk.body, at),
	}
	if a, ok := blk.body.attr("name"); ok {
		if v := ctx.eval(a.expr); v.st == Known && v.kind == cvalScalar && v.s != "" {
			c.Name = v.s
		}
	}
	c.Kind, c.Engine, c.Version = classifyResource(ctx, resType, blk.body, at)

	// Every resource gets the generic treatment, so a type nobody taught this
	// reader about still arrives with its name, its address and what it
	// declares. Then the types this reader does know get their typed fields on
	// top.
	c.Attrs = genericAttrs(ctx, resType, blk.body, file, "")
	switch resType {
	case "azurerm_container_app":
		readContainerApp(ctx, &c, blk.body, file)
	}
	out.Components = append(out.Components, c)
}

// resourceKinds maps a resource type to what it is, for the types whose kind
// is decided by the type alone.
//
// The kind vocabulary is engine/internal/detect's, on purpose, so that a
// fidelity report can compare what this says production holds against what
// detect says a compose file holds. See the Kind doc comment.
var resourceKinds = map[string]Kind{
	// Postgres.
	"azurerm_postgresql_flexible_server": KindPostgres,
	"azurerm_postgresql_server":          KindPostgres,
	"aws_rds_cluster_instance":           KindPostgres,
	// MySQL.
	"azurerm_mysql_flexible_server": KindMySQL,
	"azurerm_mysql_server":          KindMySQL,
	// Redis.
	"azurerm_redis_cache":               KindRedis,
	"aws_elasticache_cluster":           KindRedis,
	"aws_elasticache_replication_group": KindRedis,
	"aws_elasticache_serverless_cache":  KindRedis,
	"google_redis_instance":             KindRedis,
	// MongoDB.
	"aws_docdb_cluster":      KindMongoDB,
	"aws_documentdb_cluster": KindMongoDB,
	// Kafka.
	"aws_msk_cluster":            KindKafka,
	"aws_msk_serverless_cluster": KindKafka,
	"confluent_kafka_cluster":    KindKafka,
	// Elasticsearch and its fork, which detect already calls the same thing.
	"aws_elasticsearch_domain": KindElasticsearch,
	"aws_opensearch_domain":    KindElasticsearch,
	// RabbitMQ.
	"aws_mq_broker": KindRabbitMQ,
	// Object stores.
	"aws_s3_bucket":             KindObjectStore,
	"google_storage_bucket":     KindObjectStore,
	"azurerm_storage_container": KindObjectStore,
	// Queues.
	"aws_sqs_queue":              KindQueue,
	"azurerm_servicebus_queue":   KindQueue,
	"azurerm_storage_queue":      KindQueue,
	"google_pubsub_subscription": KindQueue,
	// Topics.
	"aws_sns_topic":            KindTopic,
	"azurerm_servicebus_topic": KindTopic,
	"azurerm_eventgrid_topic":  KindTopic,
	"google_pubsub_topic":      KindTopic,
	// Tables.
	"aws_dynamodb_table":             KindTable,
	"azurerm_cosmosdb_sql_container": KindTable,
	"azurerm_storage_table":          KindTable,
	"google_bigtable_table":          KindTable,
	// Secrets. The component records that the secret EXISTS and what it is
	// called. sensitive.go is what stops it recording what is in it.
	"aws_secretsmanager_secret":            KindSecret,
	"azurerm_key_vault_secret":             KindSecret,
	"google_secret_manager_secret":         KindSecret,
	"kubernetes_secret":                    KindSecret,
	"aws_secretsmanager_secret_version":    KindSecret,
	"google_secret_manager_secret_version": KindSecret,
	// Parameters.
	"aws_ssm_parameter":             KindParameter,
	"azurerm_app_configuration_key": KindParameter,
	// Services.
	"azurerm_container_app":       KindService,
	"azurerm_linux_web_app":       KindService,
	"azurerm_windows_web_app":     KindService,
	"azurerm_container_group":     KindService,
	"aws_ecs_service":             KindService,
	"aws_ecs_task_definition":     KindService,
	"aws_lambda_function":         KindService,
	"aws_apprunner_service":       KindService,
	"google_cloud_run_service":    KindService,
	"google_cloud_run_v2_service": KindService,
	"kubernetes_deployment":       KindService,
	"kubernetes_deployment_v1":    KindService,
	"kubernetes_stateful_set":     KindService,
}

// engineFamilies maps what a provider calls a database engine to detect's
// word for it.
var engineFamilies = map[string]Kind{
	"postgres": KindPostgres, "postgresql": KindPostgres,
	"aurora-postgresql": KindPostgres, "aurora_postgresql": KindPostgres,
	"mysql": KindMySQL, "mariadb": KindMySQL,
	"aurora": KindMySQL, "aurora-mysql": KindMySQL,
	"redis": KindRedis, "valkey": KindRedis,
	"rabbitmq": KindRabbitMQ, "activemq": KindRabbitMQ,
	"docdb": KindMongoDB, "mongodb": KindMongoDB,
}

// classifyResource decides a resource's kind, engine and version.
//
// A type whose kind is decided by an ATTRIBUTE rather than by the type gets
// that attribute read: aws_db_instance is a Postgres or a MySQL depending on
// what `engine` says, and an aws_db_instance whose engine this reader cannot
// resolve comes back as KindUnknown with an Unreadable Engine, which is the
// honest answer and not a guess at the commoner of the two.
func classifyResource(ctx *evalCtx, resType string, body hclBody, at Position) (Kind, Value[string], Value[string]) {
	var engine, version Value[string]
	if a, ok := body.attr("engine"); ok {
		engine = ctx.eval(a.expr).str()
	}
	if a, ok := body.attr("engine_version"); ok {
		version = ctx.eval(a.expr).str()
	} else if a, ok := body.attr("version"); ok {
		version = ctx.eval(a.expr).str()
	}

	if kind, ok := resourceKinds[resType]; ok {
		if engine.State() == Absent {
			if k := engineOfKind(kind); k != "" {
				engine = Resolved(k, at)
			}
		}
		return kind, engine, version
	}
	switch resType {
	case "aws_db_instance", "aws_rds_cluster", "aws_elasticache_cluster", "aws_mq_broker":
		if got, ok := engine.Get(); ok {
			if kind, known := engineFamilies[strings.ToLower(got)]; known {
				return kind, engine, version
			}
			return KindUnknown, engine, version
		}
		return KindUnknown, engine, version
	case "google_sql_database_instance":
		// google writes "POSTGRES_16" and "MYSQL_8_0" in one attribute, so
		// the engine and the version are read out of the same string.
		if a, ok := body.attr("database_version"); ok {
			v := ctx.eval(a.expr).str()
			if got, known := v.Get(); known {
				name, ver, _ := strings.Cut(got, "_")
				kind, ok := engineFamilies[strings.ToLower(name)]
				if !ok {
					return KindUnknown, Resolved(strings.ToLower(name), v.At()), Resolved(strings.ReplaceAll(ver, "_", "."), v.At())
				}
				return kind, Resolved(string(kind), v.At()), Resolved(strings.ReplaceAll(ver, "_", "."), v.At())
			}
			return KindUnknown, v, v
		}
	}
	return KindUnknown, engine, version
}

// engineOfKind is the engine name for a kind whose type already settled it, so
// that an azurerm_postgresql_flexible_server reports engine postgres without
// the configuration having to say so.
func engineOfKind(k Kind) string {
	switch k {
	case KindPostgres, KindMySQL, KindRedis, KindMongoDB, KindKafka,
		KindElasticsearch, KindRabbitMQ, KindClickHouse:
		return string(k)
	}
	return ""
}

// genericAttrs flattens a resource body into dotted attributes.
//
// This is what makes the reader useful on a resource type nobody has taught it
// about: the component still carries what the configuration declares, under
// the names the configuration uses, with every value scrubbed. Nested blocks
// are dotted, so `sku { tier = "GP" }` arrives as `sku.tier`.
// metaArguments are Terraform's own keywords rather than attributes of the
// resource.
//
// THEY ARE EXCLUDED BECAUSE THEY WERE DROWNING THE SIGNAL, which is a defect
// the dogfood run found and a unit test never would have. Pointed at this
// repository's control plane module the reader produced 279 unmeasured items,
// and most of them were `depends_on.0`, `lifecycle.ignore_changes.1` and
// `lifecycle.precondition.condition`: expressions that are references and
// conditions BY DEFINITION, that no reader could ever resolve, and that say
// nothing about what production holds. An unmeasured list is this package's
// most important output, and a list that is mostly noise is one nobody reads,
// which costs more than the handful of real holes buried in it were worth.
//
// count and for_each are excluded for a different reason: they are not holes,
// they are already reported, as Component.Present.
var metaArguments = map[string]bool{
	"count": true, "for_each": true, "depends_on": true, "provider": true,
	"lifecycle": true, "provisioner": true, "connection": true,
}

func genericAttrs(ctx *evalCtx, resType string, body hclBody, file, prefix string) []Attr {
	var out []Attr
	for _, a := range body.attrs {
		if prefix == "" && metaArguments[a.name] {
			continue
		}
		name := prefix + a.name
		at := Position{File: file, Line: a.pos.Line, Col: a.pos.Col}
		v := ctx.eval(a.expr)
		switch {
		case v.st == Known && v.kind == cvalObject:
			// An object attribute is flattened the same way a nested block is,
			// so `tags = { env = "prod" }` and a `tags` block read alike.
			for _, key := range v.keys {
				out = append(out, Attr{
					Name:  name + "." + key,
					Value: scrub(resType, name+"."+key, valueAt(v.obj[key], at)),
					At:    at,
				})
			}
		case v.st == Known && v.kind == cvalList:
			for i, elem := range v.list {
				out = append(out, Attr{
					Name:  name + "." + itoa(i),
					Value: scrub(resType, name, valueAt(elem, at)),
					At:    at,
				})
			}
		default:
			out = append(out, Attr{Name: name, Value: scrub(resType, name, valueAt(v, at)), At: at})
		}
	}
	for _, blk := range body.blocks {
		if prefix == "" && metaArguments[blk.typ] {
			continue
		}
		// A dynamic block is flattened under the name of the block it
		// GENERATES, not under "dynamic". Otherwise the same setting arrives
		// as `high_availability.mode` in one configuration and
		// `dynamic.high_availability.content.mode` in another, purely because
		// one author made it conditional, and a consumer comparing two
		// environments would see a difference that is not there.
		if gen, ok := blk.generated(); ok {
			prev := ctx.enterBlock(gen)
			out = append(out, genericAttrs(ctx, resType, gen.body, file, prefix+gen.typ+".")...)
			ctx.iterator = prev
			continue
		}
		label := blk.typ
		if len(blk.labels) > 0 {
			label += "." + strings.Join(blk.labels, ".")
		}
		out = append(out, genericAttrs(ctx, resType, blk.body, file, prefix+label+".")...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// valueAt turns an evaluated expression into a Value, pinning the position to
// where the attribute is written when the expression itself carries none.
func valueAt(v cval, at Position) Value[string] {
	s := v.str()
	if s.At().File == "" {
		switch s.State() {
		case Known:
			got, _ := s.Get()
			return Resolved(got, at)
		case Unreadable:
			return Unresolved[string](s.Why(), at)
		case Withheld:
			return Refused[string](s.Why(), at)
		}
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
