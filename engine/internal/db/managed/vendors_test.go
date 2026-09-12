package managed

import (
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The plan's row, written out here rather than counted from the registry.
//
// Counting the registry would make this test agree with whatever the registry
// says, which is the shape of check that cannot say no: a vendor deleted in a
// merge would take its own row out of the expectation with it and the count
// would still match. The list is the CONTRACT, copied from the L2.7 row of
// enterprise_plan.md, and the registry is what is checked against it.
var thePlansThirteen = []string{
	"aiven",
	"crunchy-bridge",
	"digitalocean",
	"fly",
	"heroku",
	"nile",
	"planetscale",
	"prisma",
	"railway",
	"render",
	"tembo",
	"timescale",
	"xata",
}

func TestTheRegistryCoversThePlansThirteen(t *testing.T) {
	var got []string
	for _, v := range All() {
		got = append(got, v.Name)
	}
	sort.Strings(got)
	want := append([]string(nil), thePlansThirteen...)
	sort.Strings(want)
	require.Equal(t, want, got,
		"the L2.7 row names thirteen vendors and every one of them has to be placed. A "+
			"vendor missing here is one nobody decided about, and a vendor here that the "+
			"row does not name is scope this lane did not have.")
}

func TestRecognizeMatchesOnALabelBoundary(t *testing.T) {
	v, ok := Recognize("pg-service.aivencloud.com")
	require.True(t, ok, "a subdomain of a vendor's suffix is that vendor")
	require.Equal(t, "aiven", v.Name)

	v, ok = Recognize("aivencloud.com:5432")
	require.True(t, ok, "the argument carries a port because every caller has one")
	require.Equal(t, "aiven", v.Name)

	// The one that matters. A plain suffix match would call this Aiven, and
	// the answer drives a refusal, so a wrong recognition refuses somebody
	// whose own server works.
	_, ok = Recognize("notaivencloud.com")
	require.False(t, ok,
		"a host that merely ENDS in the vendor's suffix without a label boundary is "+
			"somebody else's server, and recognising it would attach the vendor's refusal "+
			"to a setup that works")

	_, ok = Recognize("")
	require.False(t, ok, "an empty host is unknown rather than the first vendor in the list")
}

func TestHerokuIsNotRecognisedFromItsHost(t *testing.T) {
	// Heroku is the vendor with the strongest documented refusal on this row
	// and the one that cannot be recognised, and the two facts pull in
	// opposite directions, which is exactly why this is asserted rather than
	// left to the general rule. A Heroku Postgres host is an EC2 name. Wiring
	// a suffix for it would call every self hosted Postgres on EC2 a Heroku
	// database and refuse it.
	_, ok := Recognize("ec2-203-0-113-1.compute-1.amazonaws.com")
	require.False(t, ok,
		"a Heroku Postgres host is an EC2 name that any machine on EC2 also carries")

	var heroku Vendor
	for _, v := range All() {
		if v.Name == "heroku" {
			heroku = v
		}
	}
	require.Empty(t, heroku.Hosts, "heroku carries no host suffix")
	require.NotEmpty(t, heroku.HostsUnknown, "and it says why rather than leaving the gap silent")
}

func TestCopyOnWriteIsDeclaredOnlyWithACopyOnWriteMechanism(t *testing.T) {
	// The same self consistency the datastore suite makes about a provider,
	// made here about a vendor record. Copy on write is the distinguishing
	// commercial claim of this whole wave and the field that
	// engine/conformance/cow.go now exists to falsify on a provider. Nothing
	// falsifies it on a VENDOR record, because no account was opened, so the
	// least this file can do is refuse to say it about a mechanism that is not
	// one.
	for _, v := range All() {
		if v.CopyOnWrite {
			require.Equal(t, MechanismCopyOnWrite, v.Mechanism,
				"%s declares copy on write with mechanism %q. A fork restored from a "+
					"backup copies the bytes and its clock grows with them, which is the "+
					"opposite of the claim.", v.Name, v.Mechanism)
		}
		if v.Mechanism == MechanismCopyOnWrite {
			require.True(t, v.CopyOnWrite,
				"%s is classed as a copy on write branch and does not declare it", v.Name)
		}
	}
}

func TestEveryVerdictCarriesItsEvidence(t *testing.T) {
	for _, v := range All() {
		require.NotEmpty(t, v.Display, "%s has no display name", v.Name)
		require.NotEmpty(t, v.Mechanism, "%s has no mechanism", v.Name)
		require.NotEmpty(t, v.MechanismCitation.URL, "%s cites no page for its mechanism", v.Name)
		require.NotEmpty(t, v.MechanismCitation.Retrieved,
			"%s cites a page with no date, so nobody can tell whether it has gone stale", v.Name)

		for label, verdict := range map[string]Verdict{
			"HostServer": v.HostServer,
			"SourceURL":  v.SourceURL,
		} {
			require.Contains(t, []Answer{Yes, No, Unverified}, verdict.Answer,
				"%s %s has answer %q, which is not one of the three", v.Name, label, verdict.Answer)
			require.NotEmpty(t, verdict.Reason, "%s %s answers with no reason", v.Name, label)
			require.NotEmpty(t, verdict.Citation.URL, "%s %s cites no page", v.Name, label)
			require.NotEmpty(t, verdict.Citation.Retrieved, "%s %s cites no date", v.Name, label)
			if verdict.Answer != Unverified {
				require.NotEmpty(t, verdict.Citation.Quote,
					"%s %s answers %q, and a yes or a no rests on something the vendor "+
						"actually said. Unverified is the answer for a page that did not "+
						"say, and it is there so that silence never has to be written up "+
						"as a decision.", v.Name, label, verdict.Answer)
			}
		}
	}
}

func TestAHostIsRecordedOrItsAbsenceIs(t *testing.T) {
	for _, v := range All() {
		switch {
		case len(v.Hosts) > 0 && v.HostsUnknown != "":
			t.Fatalf("%s carries host suffixes AND a reason it has none, which is a "+
				"contradiction a reader would resolve in whichever direction they read "+
				"first", v.Name)
		case len(v.Hosts) == 0 && v.HostsUnknown == "":
			t.Fatalf("%s carries no host suffix and no reason. An unexplained gap reads "+
				"as work not done, and here it is sometimes the answer: Heroku cannot be "+
				"recognised at all.", v.Name)
		}
		for _, suffix := range v.Hosts {
			require.NotEqual(t, ".", suffix[:1],
				"%s stores %q with a leading dot, and the boundary is added by the matcher",
				v.Name, suffix)
			require.Equal(t, strings.ToLower(suffix), suffix,
				"%s stores %q with an uppercase letter, and the matcher lowercases the "+
					"host it is given rather than the table", v.Name, suffix)
		}
	}
}

func TestNoVendorSuffixSwallowsAnother(t *testing.T) {
	// Two vendors, one of whose suffixes is a suffix of the other's, would make
	// Recognize return whichever came first in the registry, and the answer
	// would depend on the order of a literal nobody thinks of as ordered.
	type entry struct {
		vendor string
		suffix string
	}
	var all []entry
	for _, v := range All() {
		for _, s := range v.Hosts {
			all = append(all, entry{v.Name, s})
		}
	}
	for _, a := range all {
		for _, b := range all {
			if a.vendor == b.vendor {
				continue
			}
			require.False(t, a.suffix == b.suffix || strings.HasSuffix(a.suffix, "."+b.suffix),
				"%s claims %q and %s claims %q, so a host matching the first also matches "+
					"the second and Recognize answers by registry order",
				a.vendor, a.suffix, b.vendor, b.suffix)
		}
	}
}
