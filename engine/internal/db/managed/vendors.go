// Package managed answers one question per managed Postgres vendor: what, if
// anything, this repository has to build for it.
//
// The plan gave one row to thirteen vendors, and the failure that row is
// written against is thirteen thin providers that each call pgurl and add a
// name to a list. That is code volume wearing the clothes of coverage: it
// compiles, it looks like thirteen integrations, and it does nothing the one
// provider underneath was not already doing. So the deliverable here is a
// DECISION per vendor with the evidence attached, and the decision for most of
// them is that there is nothing to build.
//
// Three questions are asked of every vendor and all three are recorded, because
// the interesting answers are not the same question in each case:
//
//   - Does the vendor have a NATIVE branching mechanism, and is it really copy
//     on write, or a fork from a backup wearing the word? Mechanism and
//     CopyOnWrite.
//   - Can the vendor's own instance be the pgurl provider's HOST SERVER, which
//     needs a role that may CREATE DATABASE? HostServer.
//   - Does the vendor hand out a connection string pg_dump can read, which is
//     what makes it usable as a SOURCE? SourceURL.
//
// The second and third are separate on purpose and the difference is the whole
// point of the pgurl provider's design. The vendor is the SOURCE, read once per
// refresh by pg_dump, and pg_dump needs nothing but read access. The HOST
// SERVER, where the goldens and the branches are made, is a different server
// that the pgurl provider's own header says should not be production. So a
// vendor that refuses CREATE DATABASE is not a vendor Antifailure cannot serve;
// it is a vendor that cannot ALSO be the host server, which is a smaller and
// truer claim than the one a table of thirteen ticks would have made.
//
// EVERY VERDICT CARRIES ITS CITATION, and the citation carries the date it was
// read. These are other people's products and they change: Tembo's managed
// Postgres was withdrawn between this file's subject being chosen and this file
// being written, and Tiger Cloud's fork means two different things on two of
// its own tiers. A verdict with no source is an opinion that will be quoted as
// a fact, and a verdict with no date is one nobody can tell has gone stale.
//
// WHAT WAS NOT PROVED. Every citation here is the vendor's own published
// documentation, read at the date recorded beside it. No account was created on
// any of these thirteen services, no request was sent to any of their control
// planes, and no database was branched on any of them. So this file records
// what each vendor SAYS its product does. It does not record what any of them
// did, and no number in it was measured. The refusal is the deliverable rather
// than an apology for one: an invented seconds figure for a service nobody
// connected to would be worth less than a blank cell, because a blank cell
// cannot be quoted.
package managed

import (
	"sort"
	"strings"
)

// Mechanism is the class of the vendor's own way of making a second copy of a
// database, named for what it IS rather than for what the vendor calls it.
//
// The distinction the names carry is the one a buyer pays for. "Fork" is the
// word six of these vendors use and it means a restore from a backup at five
// of them, so classing by the vendor's vocabulary would have put a thirty
// second operation and a two hour one in the same box.
type Mechanism string

const (
	// MechanismCopyOnWrite is a branch that shares storage with its parent, so
	// its creation time does not grow with the database.
	MechanismCopyOnWrite Mechanism = "copy on write branch"
	// MechanismForkFromBackup is a new instance restored from the source's
	// backup, optionally to a point in time. It is faster than a dump and a
	// restore because the vendor already holds the backup, and it is not
	// branching: the bytes are copied and the clock grows with them.
	MechanismForkFromBackup Mechanism = "fork restored from a backup"
	// MechanismRestoreToNewInstance is a point in time recovery that lands in a
	// new instance. Mechanically it is the row above, and it is separate
	// because the vendor offers it as recovery rather than as a copy: there is
	// no create fork call, only a recover button.
	MechanismRestoreToNewInstance Mechanism = "point in time recovery into a new instance"
	// MechanismEmptyBranch is a branch that carries no data. It is a real
	// product feature and it is useless to this engine, whose branch has to
	// hold the masked golden.
	MechanismEmptyBranch Mechanism = "branch created empty"
	// MechanismNone is a vendor with no documented way to make a second copy of
	// a live database.
	MechanismNone Mechanism = "none documented"
	// MechanismWithdrawn is a vendor whose managed Postgres no longer exists.
	MechanismWithdrawn Mechanism = "the product was withdrawn"
)

// Answer is a three valued verdict, and the third value is the point of it.
//
// A boolean would have forced every unverifiable question into a yes or a no,
// and the direction it would have been forced in is the flattering one: a table
// of thirteen ticks reads as coverage. Unverified is a first class answer here
// for the same reason this repository makes "I could not look" distinct from
// "I looked and it was fine" everywhere else, and it is spelled out in the
// documentation page rather than rendered as a blank cell.
type Answer string

const (
	// Yes means the vendor's own documentation says so, and Citation quotes it.
	Yes Answer = "yes"
	// No means the vendor's own documentation says the opposite, and Citation
	// quotes that.
	No Answer = "no"
	// Unverified means the vendor's published documentation did not answer it
	// at the date it was read. It is not a guess in either direction.
	Unverified Answer = "unverified"
)

// Citation is where a verdict came from and when it was read.
type Citation struct {
	// URL is the page.
	URL string
	// Retrieved is the ISO date it was read, so a reader can tell how old the
	// verdict is without trusting the file's own commit date.
	Retrieved string
	// Quote is what the page says, close to the vendor's own words.
	//
	// Close rather than character exact, and the departure is declared here
	// rather than left for a reader to discover. Two things are done to it:
	// punctuation is normalised to this repository's writing rules, which ban
	// the em dash and the double hyphen everywhere including inside a string
	// literal, and a passage spread over several sentences is condensed into
	// one. Neither is allowed to change the claim, and URL is the authority: a
	// reader checking a verdict reads the page, not this field.
	//
	// Empty when the verdict is that the documentation says nothing, because
	// there is no sentence to quote for an absence and inventing a paraphrase
	// to fill the field would make silence look like a statement.
	Quote string
}

// Verdict is one answer with its evidence.
type Verdict struct {
	Answer   Answer
	Reason   string
	Citation Citation
}

// Vendor is one managed Postgres product and what this repository decided
// about it.
type Vendor struct {
	// Name is the short identifier used in messages and in the docs table.
	Name string
	// Display is the product's own name.
	Display string
	// Hosts are the connection string host suffixes that identify this vendor,
	// matched on a label boundary. Empty means the vendor cannot be recognised
	// from a connection string, and HostsUnknown says why.
	Hosts []string
	// HostsUnknown is why Hosts is empty, and it is recorded rather than left
	// implicit because the two reasons are different in kind. Heroku's hosts
	// are EC2 names that any machine on EC2 also has, so recognition there is
	// impossible rather than merely undone. The others are vendors whose
	// documentation published no connection string example that was found.
	HostsUnknown string

	// Mechanism is the class of the vendor's own copying feature.
	Mechanism Mechanism
	// CopyOnWrite is whether that mechanism really shares storage. It is the
	// field engine/conformance/cow.go can now falsify on a provider, and it is
	// recorded here for the vendors no provider was written for so that the
	// comparison table is not a row of the word "fork" thirteen times.
	CopyOnWrite bool
	// MechanismCitation is the evidence for the two fields above.
	MechanismCitation Citation

	// HostServer is whether the vendor's own instance can hold the goldens and
	// the branches, which needs a role that may CREATE DATABASE.
	HostServer Verdict
	// SourceURL is whether the vendor hands out a connection string pg_dump can
	// read, which is what it takes to be a source.
	SourceURL Verdict
}

// registry is the thirteen, in the order the plan's row lists them.
//
// Sorted for output by Name in All rather than here, so that the file reads
// against the plan and the output reads alphabetically.
var registry = []Vendor{
	{
		Name:    "crunchy-bridge",
		Display: "Crunchy Bridge",
		Hosts:   []string{"db.postgresbridge.com"},

		Mechanism:   MechanismForkFromBackup,
		CopyOnWrite: false,
		MechanismCitation: Citation{
			URL:       "https://docs.crunchybridge.com/api/cluster",
			Retrieved: "2026-09-08",
			Quote: "POST /clusters/{cluster_id}/forks creates a point in time copy of an " +
				"existing cluster, and clusters cannot be forked until an initial backup " +
				"has been captured for them.",
		},

		HostServer: Verdict{
			Answer: Yes,
			Reason: "the postgres role Crunchy Bridge provisions is a superuser, so it may create databases",
			Citation: Citation{
				URL:       "https://docs.crunchybridge.com/concepts/users",
				Retrieved: "2026-09-08",
				Quote: "New instances start with two roles by default: application, a non " +
					"superuser suitable for use in application code, and postgres, a " +
					"superuser capable of performing any action in a database.",
			},
		},
		SourceURL: Verdict{
			Answer: Yes,
			Reason: "a plain postgres:// URL, SSL required",
			Citation: Citation{
				URL:       "https://docs.crunchybridge.com/connecting/psql",
				Retrieved: "2026-09-08",
				Quote:     "psql postgres://postgres:PASSWORD@p.EXAMPLE.db.postgresbridge.com:5432/meteors",
			},
		},
	},
	{
		Name:    "timescale",
		Display: "Tiger Cloud, formerly Timescale Cloud",
		Hosts:   []string{"tsdb.cloud.timescale.com", "tsdb.cloud.tigerdata.com"},

		// The one vendor here whose answer differs between its own tiers, and
		// the split is recorded rather than rounded. A free service forks
		// through copy on write in thirty to ninety seconds; a standard service
		// forks by restoring a backup and replaying WAL in five to twenty
		// minutes or more. Rounding that to "copy on write" would publish the
		// free tier's number for the paid product, and rounding it the other
		// way would hide a real capability, so the class is the one the paying
		// customer gets and the exception is in the citation.
		Mechanism:   MechanismForkFromBackup,
		CopyOnWrite: false,
		MechanismCitation: Citation{
			URL:       "https://www.tigerdata.com/docs/use-timescale/latest/fork-services",
			Retrieved: "2026-09-08",
			Quote: "Free services use a Copy on Write storage architecture with zero copy " +
				"between a fork and the parent, taking about 30 to 90 seconds. Standard " +
				"services use traditional storage architecture with backup restore plus " +
				"WAL replay, which varies with the size of your service, typically 5 to " +
				"20 or more minutes.",
		},

		HostServer: Verdict{
			Answer: No,
			Reason: "a Tiger Cloud service holds exactly one database and tsdbadmin is not a superuser",
			Citation: Citation{
				URL:       "https://www.tigerdata.com/docs/use-timescale/latest/services/troubleshooting",
				Retrieved: "2026-09-08",
				Quote: "You cannot create multiple databases in a single service. If you " +
					"need another database, create a new service.",
			},
		},
		SourceURL: Verdict{
			Answer: Yes,
			Reason: "a plain postgres:// URL on a per service port",
			Citation: Citation{
				URL:       "https://www.tigerdata.com/docs/integrate/find-connection-details",
				Retrieved: "2026-09-08",
				Quote:     "postgres://USERNAME:PASSWORD@service.project.tsdb.cloud.timescale.com:30477/tsdb?sslmode=require",
			},
		},
	},
	{
		Name:    "aiven",
		Display: "Aiven for PostgreSQL",
		Hosts:   []string{"aivencloud.com"},

		Mechanism:   MechanismForkFromBackup,
		CopyOnWrite: false,
		MechanismCitation: Citation{
			URL:       "https://aiven.io/docs/platform/concepts/service-forking",
			Retrieved: "2026-09-08",
			Quote: "Fork an Aiven service to create a complete and independent copy of it " +
				"from its latest backup. During the forking process, the fork might " +
				"initially have only one node while backups are being taken.",
		},

		HostServer: Verdict{
			Answer: Yes,
			Reason: "additional databases may be created in an existing service, and avnadmin may create them",
			Citation: Citation{
				URL:       "https://aiven.io/docs/products/postgresql/howto/create-database",
				Retrieved: "2026-09-08",
				Quote: "Once you have created your Aiven for PostgreSQL service, you can " +
					"add additional databases, whether for security purposes or to " +
					"isolate your data per application.",
			},
		},
		SourceURL: Verdict{
			Answer: Yes,
			Reason: "a plain postgres:// URL on a per service port",
			Citation: Citation{
				URL:       "https://aiven.io/docs/tools/cli/service/connection-info",
				Retrieved: "2026-09-08",
				Quote:     "postgres://avnadmin:PASSWORD@pg-service.aivencloud.com:12345/defaultdb?sslmode=require",
			},
		},
	},
	{
		Name:    "render",
		Display: "Render Postgres",
		Hosts:   []string{"render.com"},

		Mechanism:   MechanismRestoreToNewInstance,
		CopyOnWrite: false,
		MechanismCitation: Citation{
			URL:       "https://render.com/docs/postgresql-backups",
			Retrieved: "2026-09-08",
			Quote: "When you trigger point in time recovery, Render spins up a new " +
				"database instance that reflects your original instance's state at a " +
				"specified time in the past.",
		},

		HostServer: Verdict{
			Answer: Yes,
			Reason: "the documentation tells you to run CREATE DATABASE in psql against the instance",
			Citation: Citation{
				URL:       "https://render.com/docs/postgresql-creating-connecting",
				Retrieved: "2026-09-08",
				Quote: "You can create additional databases in your Render Postgres " +
					"instance by opening a psql session to your instance and running " +
					"CREATE DATABASE.",
			},
		},
		SourceURL: Verdict{
			Answer: Yes,
			Reason: "the external database URL is a plain postgres:// URL",
			Citation: Citation{
				URL:       "https://render.com/docs/postgresql-creating-connecting",
				Retrieved: "2026-09-08",
				Quote:     "postgres://USER:PASSWORD@dpg-EXAMPLE-a.frankfurt-postgres.render.com/DATABASE",
			},
		},
	},
	{
		Name:    "railway",
		Display: "Railway Postgres",
		Hosts:   []string{"proxy.rlwy.net", "railway.internal"},

		Mechanism:   MechanismNone,
		CopyOnWrite: false,
		MechanismCitation: Citation{
			URL:       "https://docs.railway.com/databases/postgresql",
			Retrieved: "2026-09-08",
			// No quote, because the verdict is an absence. Railway's Postgres
			// page documents deployment, connection and backups and says
			// nothing about forking or branching a database, and the
			// environment fork the platform does offer copies services rather
			// than data.
		},

		HostServer: Verdict{
			Answer: Yes,
			Reason: "Railway runs the official Postgres image, whose default user is the superuser postgres",
			Citation: Citation{
				URL:       "https://docs.railway.com/databases/build-a-database-service",
				Retrieved: "2026-09-08",
				Quote: "POSTGRES_USER is optional with a default value of postgres, the " +
					"superuser account created when PostgreSQL is initialized.",
			},
		},
		SourceURL: Verdict{
			Answer: Yes,
			Reason: "DATABASE_PUBLIC_URL is a plain postgres:// URL through a TCP proxy",
			Citation: Citation{
				URL:       "https://docs.railway.com/databases/postgresql",
				Retrieved: "2026-09-08",
				Quote: "Adding public access creates a TCP proxy and populates a " +
					"DATABASE_PUBLIC_URL variable with the external connection string, " +
					"on a proxy domain ending in proxy.rlwy.net.",
			},
		},
	},
	{
		Name:         "fly",
		Display:      "Fly Managed Postgres",
		HostsUnknown: "no connection string example was found in the published Managed Postgres documentation at the date it was read",

		Mechanism: MechanismForkFromBackup,
		// Recorded false, and the reason is that the documentation does not
		// say. Fly publishes the fork endpoint and says the fork is
		// provisioned asynchronously and inherits the source's settings, and
		// says nothing about shared storage. An undocumented mechanism is not
		// evidence of copy on write, and this field is the one the conformance
		// suite can now refuse, so the direction that does not invent a claim
		// is false with the silence recorded.
		CopyOnWrite: false,
		MechanismCitation: Citation{
			URL:       "https://docs.machines.dev/postgres-clusters/Postgres_fork",
			Retrieved: "2026-09-08",
			Quote: "POST /v1/postgres/{postgres_cluster_id}/fork. The fork inherits the " +
				"source's settings, the source cluster is left unchanged, and the fork " +
				"is provisioned asynchronously.",
		},

		HostServer: Verdict{
			Answer: Unverified,
			Reason: "additional databases are documented as a dashboard and flyctl action, and no published statement says a SQL role carries CREATEDB",
			Citation: Citation{
				URL:       "https://fly.io/docs/mpg/cluster-configuration/",
				Retrieved: "2026-09-08",
				Quote: "You can create additional databases from the dashboard or through " +
					"flyctl. The Schema Admin role is the closest to a superuser role.",
			},
		},
		SourceURL: Verdict{
			Answer: Unverified,
			Reason: "the connection string is generated per user from the dashboard and no example form was published",
			Citation: Citation{
				URL:       "https://fly.io/docs/mpg/cluster-configuration/",
				Retrieved: "2026-09-08",
				Quote: "Once the user has been created, you can generate a connection " +
					"string for them from the Connect tab of your dashboard.",
			},
		},
	},
	{
		Name:    "digitalocean",
		Display: "DigitalOcean Managed Databases for PostgreSQL",
		Hosts:   []string{"db.ondigitalocean.com"},

		Mechanism:   MechanismForkFromBackup,
		CopyOnWrite: false,
		MechanismCitation: Citation{
			URL:       "https://docs.digitalocean.com/products/databases/postgresql/how-to/fork-clusters/",
			Retrieved: "2026-09-08",
			Quote: "Forking is a cluster level action that copies the source cluster's " +
				"data and configuration settings. Creating a fork takes longer than " +
				"creating a new cluster because the source cluster's data and " +
				"configuration settings are copied to the fork during provisioning.",
		},

		HostServer: Verdict{
			Answer: Yes,
			Reason: "a cluster holds many databases and doadmin may add them",
			Citation: Citation{
				URL:       "https://docs.digitalocean.com/products/databases/postgresql/how-to/manage-users-and-databases/",
				Retrieved: "2026-09-08",
				Quote: "PostgreSQL database clusters come configured with a default " +
					"database and a default administrative user, doadmin. You can add " +
					"additional users and databases.",
			},
		},
		SourceURL: Verdict{
			Answer: Yes,
			Reason: "a plain postgres:// URL on port 25060",
			Citation: Citation{
				URL:       "https://docs.digitalocean.com/products/databases/postgresql/how-to/connect/",
				Retrieved: "2026-09-08",
				Quote:     "postgres://doadmin:PASSWORD@EXAMPLE-do-user-0.db.ondigitalocean.com:25060/defaultdb?sslmode=require",
			},
		},
	},
	{
		Name:    "heroku",
		Display: "Heroku Postgres",
		// The one vendor here that cannot be recognised at all, and it is a
		// property of the product rather than a gap in the reading. A Heroku
		// Postgres host is an EC2 name of the form
		// ec2-ADDRESS.compute-1.amazonaws.com, which is the name every other
		// machine on EC2 also has. A suffix that matched it would call every
		// self hosted Postgres on an EC2 instance a Heroku database, and a
		// wrong recognition is worse than none: it would attach Heroku's
		// refusal to somebody whose own server does grant CREATEDB.
		HostsUnknown: "a Heroku Postgres host is an EC2 name that any machine on EC2 also carries, so no suffix can identify one without claiming every EC2 host",

		Mechanism:   MechanismForkFromBackup,
		CopyOnWrite: false,
		MechanismCitation: Citation{
			URL:       "https://devcenter.heroku.com/articles/heroku-postgres-fork",
			Retrieved: "2026-09-08",
			Quote: "Forking creates a new database containing a snapshot of an existing " +
				"database at the current point in time. Preparing a fork can take " +
				"anywhere from several minutes to several hours, depending on the size " +
				"of your dataset.",
		},

		HostServer: Verdict{
			Answer: No,
			Reason: "an add on provisions one database and there is no superuser, so nothing on the instance may create a second one",
			Citation: Citation{
				URL:       "https://help.heroku.com/IV1DHMS2/can-i-get-superuser-privileges-or-create-a-superuser-in-heroku-postgres",
				Retrieved: "2026-09-08",
				Quote: "Heroku Postgres does not provide a superuser role for your " +
					"database, for the security and stability of all customers. Every " +
					"newly provisioned database includes a default credential named " +
					"owner, a permissive role one step below superuser.",
			},
		},
		SourceURL: Verdict{
			Answer: Yes,
			Reason: "DATABASE_URL is a plain postgres:// URL, and its credentials rotate",
			Citation: Citation{
				URL:       "https://devcenter.heroku.com/articles/connecting-heroku-postgres",
				Retrieved: "2026-09-08",
				Quote:     "postgres://USER:PASSWORD@ec2-EXAMPLE.compute-1.amazonaws.com:5432/DATABASE",
			},
		},
	},
	{
		Name:    "planetscale",
		Display: "PlanetScale Postgres",
		Hosts:   []string{"psdb.cloud"},

		// Two mechanisms and neither is branching in the sense this engine
		// needs. The branch the Branches page makes is EMPTY, and the branch
		// made from a backup is a restore. Classed as the empty one because
		// that is what the product's own branching page produces by default,
		// and the restore is in the citation so the reader gets both.
		Mechanism:   MechanismEmptyBranch,
		CopyOnWrite: false,
		MechanismCitation: Citation{
			URL:       "https://planetscale.com/docs/postgres/branching",
			Retrieved: "2026-09-08",
			Quote: "You can currently create a new empty branch with no schema and no " +
				"data, or create a branch from a backup, which includes schema and data. " +
				"There is no data replication between branches, so schema and data need " +
				"to be copied manually.",
		},

		HostServer: Verdict{
			Answer: Yes,
			Reason: "the default postgres role is created with CREATEDB, and one cluster is documented as holding many logical databases",
			Citation: Citation{
				URL:       "https://planetscale.com/docs/postgres/connecting/roles",
				Retrieved: "2026-09-08",
				Quote: "CREATE ROLE POSTGRES_USERNAME NOSUPERUSER CREATEDB CREATEROLE " +
					"INHERIT LOGIN REPLICATION BYPASSRLS PASSWORD PASSWORD;",
			},
		},
		SourceURL: Verdict{
			Answer: Yes,
			Reason: "a plain postgres:// URL, port 5432 direct and 6432 pooled",
			Citation: Citation{
				URL:       "https://planetscale.com/docs/postgres/connecting",
				Retrieved: "2026-09-08",
				Quote: "Direct connections use port 5432 and PSBouncer uses port 6432, on " +
					"a host of the form IDENTIFIER-REGION-INSTANCE.horizon.psdb.cloud.",
			},
		},
	},
	{
		Name:         "xata",
		Display:      "Xata",
		HostsUnknown: "no connection string example was found in the published documentation at the date it was read",

		// The only genuine copy on write branch on this row, and the only one
		// of the thirteen whose mechanism would earn a provider of its own.
		Mechanism:   MechanismCopyOnWrite,
		CopyOnWrite: true,
		MechanismCitation: Citation{
			URL:       "https://xata.io/docs/core-concepts/branching",
			Retrieved: "2026-09-08",
			Quote: "Creating a child branch copies the parent's schema and data using a " +
				"Copy on Write storage snapshot, so it completes in seconds even for " +
				"terabyte scale databases.",
		},

		HostServer: Verdict{
			Answer: Unverified,
			Reason: "the self hosted platform gives you the CloudNativePG cluster and therefore the role, and no published statement covers the hosted service's role",
			Citation: Citation{
				URL:       "https://github.com/xataio/xata",
				Retrieved: "2026-09-08",
				Quote: "A cloud native platform for self hosting multiple Postgres " +
					"instances on Kubernetes, with fast branching using Copy on Write at " +
					"the storage level, built on CloudNativePG and OpenEBS.",
			},
		},
		SourceURL: Verdict{
			Answer: Unverified,
			Reason: "no connection string example was found, though the platform runs vanilla Postgres",
			Citation: Citation{
				URL:       "https://xata.io/docs/core-concepts/branching",
				Retrieved: "2026-09-08",
				Quote:     "After creation, branches are independent PostgreSQL instances.",
			},
		},
	},
	{
		Name:         "nile",
		Display:      "Nile",
		HostsUnknown: "no connection string example was found in the published documentation at the date it was read",

		Mechanism:   MechanismNone,
		CopyOnWrite: false,
		MechanismCitation: Citation{
			URL:       "https://thenile.dev/docs/support/backup_restore",
			Retrieved: "2026-09-08",
			Quote: "An appropriate backup, based on the time when the data issue " +
				"occurred, will be restored in a separate schema within your database or " +
				"alternatively into the original schema in an empty database. Restore is " +
				"not currently self serve.",
		},

		HostServer: Verdict{
			Answer: Unverified,
			Reason: "databases are created through the control plane API and no published statement covers a SQL role's CREATEDB",
			Citation: Citation{
				URL:       "https://thenile.dev/docs/api-reference/databases/create-a-database",
				Retrieved: "2026-09-08",
				Quote: "Creating a database creates a database record in the control " +
					"plane and triggers creation in the target region.",
			},
		},
		SourceURL: Verdict{
			Answer:   Unverified,
			Reason:   "no connection string example was found in the published documentation",
			Citation: Citation{URL: "https://thenile.dev/docs", Retrieved: "2026-09-08"},
		},
	},
	{
		Name:    "prisma",
		Display: "Prisma Postgres",
		Hosts:   []string{"db.prisma.io", "prisma-data.net"},

		Mechanism:   MechanismNone,
		CopyOnWrite: false,
		MechanismCitation: Citation{
			URL:       "https://www.prisma.io/docs/postgres/database/backups",
			Retrieved: "2026-09-08",
			Quote: "Automated backups: daily backups with point in time recovery. No " +
				"branching or forking of an existing database is documented.",
		},

		HostServer: Verdict{
			Answer:   Unverified,
			Reason:   "the published documentation covers connecting and does not say whether the issued role may create databases",
			Citation: Citation{URL: "https://www.prisma.io/docs/postgres/database", Retrieved: "2026-09-08"},
		},
		// The one vendor on this row whose default connection string is not a
		// connection string at all. The Prisma Console issues
		// prisma+postgres://accelerate.prisma-data.net/?api_key=..., which is
		// an HTTP protocol URL that pg_dump cannot speak, and a second direct
		// TCP string that it can. Somebody who pastes the first one gets
		// AF-DB-024 telling them the scheme is wrong, which is true and is not
		// the sentence they need.
		SourceURL: Verdict{
			Answer: Yes,
			Reason: "with the DIRECT connection string on db.prisma.io, not the prisma+postgres Console URL",
			Citation: Citation{
				URL:       "https://www.prisma.io/docs/postgres/database/connecting-to-your-database",
				Retrieved: "2026-09-08",
				Quote: "Use the direct connection string with psql, pg_dump, pg_restore " +
					"and GUI tools: postgres://USER:PASSWORD@db.prisma.io:5432/?sslmode=require",
			},
		},
	},
	{
		Name:         "tembo",
		Display:      "Tembo Cloud",
		HostsUnknown: "the product no longer exists, so there is no host to recognise",

		Mechanism:   MechanismWithdrawn,
		CopyOnWrite: false,
		MechanismCitation: Citation{
			URL:       "https://www.tembo.io/",
			Retrieved: "2026-09-08",
			Quote: "Cloud platform for running AI agents in complete, shareable software " +
				"environments. The site sells agent orchestration and no managed " +
				"Postgres product appears on it.",
		},

		HostServer: Verdict{
			Answer: No,
			Reason: "there is no service to point anything at",
			Citation: Citation{
				URL:       "https://www.tembo.io/",
				Retrieved: "2026-09-08",
				Quote: "The site sells a cloud platform for running AI agents. No managed " +
					"Postgres product is offered on it.",
			},
		},
		SourceURL: Verdict{
			Answer: No,
			Reason: "there is no service to read from",
			Citation: Citation{
				URL:       "https://www.tembo.io/",
				Retrieved: "2026-09-08",
				Quote: "The site sells a cloud platform for running AI agents. No managed " +
					"Postgres product is offered on it.",
			},
		},
	},
}

// All returns the vendors, sorted by name.
//
// A copy each call, because the registry is package level state and a caller
// that sorted or edited the slice it was handed would change what every other
// caller sees. The slice is thirteen elements; the copy is not the cost.
func All() []Vendor {
	out := make([]Vendor, len(registry))
	copy(out, registry)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Recognize returns the vendor a connection string host belongs to.
//
// The argument may carry a port, because every caller here has a host and port
// rather than a host: pgurl keeps one for messages and the CLI prints one.
//
// Matching is on a LABEL BOUNDARY, so "aivencloud.com" matches
// "pg.aivencloud.com" and does not match "notaivencloud.com". A plain
// strings.HasSuffix would match the second, and the consequence is not a
// cosmetic mislabel: the answer drives a refusal, so recognising somebody
// else's server as Heroku or Tiger Cloud would refuse a setup that works.
//
// Vendors with no host suffix are never returned. That is the honest answer for
// Heroku, whose hosts are EC2 names, and for the three whose documentation
// published no example: an unrecognised host gets the general message rather
// than a guessed vendor's.
func Recognize(hostport string) (Vendor, bool) {
	host := strings.ToLower(strings.TrimSpace(hostport))
	if host == "" {
		return Vendor{}, false
	}
	// Strip the port, if there is one. net.SplitHostPort is not used because
	// it errors on a bare host, which is a normal input here rather than a
	// fault, and because a bracketed IPv6 literal is never one of these
	// vendors.
	if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host, "]") {
		host = host[:i]
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return Vendor{}, false
	}
	for _, v := range registry {
		for _, suffix := range v.Hosts {
			if host == suffix || strings.HasSuffix(host, "."+suffix) {
				return v, true
			}
		}
	}
	return Vendor{}, false
}
