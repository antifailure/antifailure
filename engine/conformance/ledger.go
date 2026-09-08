package conformance

// The ledger, which is the second half of the ruling that produced the third
// verdict and the half that faces a customer.
//
// A three valued answer inside a test binary settles nothing on its own. The
// wave publishes one shared table of branch time per provider, a buyer chooses
// a vendor from it, and a cell in that table saying yes because somebody typed
// yes is the same defect as a capability nothing can refuse, one surface
// further out. So the rule is that the publishable value comes from a recorded
// verdict, and the recorded verdicts live here where a sweep can check both
// directions at once: no declaration without an entry, and no published claim
// without a proved entry.
//
// WHY AN ENTRY THAT SAYS UNPROVEN IS THE HONEST ENTRY FOR MOST OF THESE.
//
// Unproven is not only "the harness copies". sitesmoke's rule is that an empty
// set of findings is Undecided rather than Allowed, because a run that checked
// nothing has proved nothing, and the same rule applies to a provider whose
// instrument is armed and has never been fired. Both reach the same place: the
// declaration is not evidence a customer facing surface may print. The Because
// line is what tells the two apart, and it is why the ledger records prose
// rather than a boolean.
//
// This is deliberately uncomfortable reading. Four of the six providers that
// declare copy on write in this repository have no recorded verdict, and
// writing that down is the point: before this file the same four looked
// exactly like a provider that had been measured, because nothing anywhere
// distinguished them.

// LedgerEntry is what this repository can say about one provider's copy on
// write declaration.
type LedgerEntry struct {
	// Declared is the value the provider returns from Capabilities().
	Declared bool
	// Verdict is what a run of CopyOnWrite_BranchTimeMatchesTheDeclaration
	// recorded about that declaration. Only Proved is publishable.
	Verdict Answer
	// Because says why, and it is the only thing distinguishing a harness that
	// cannot exhibit the property from an instrument nobody has fired.
	Because string
	// Evidence is the repository path to the instrument, so that a reader can
	// go and look at what would have refused the declaration rather than
	// taking this table's word for it.
	Evidence string
}

// CopyOnWriteLedger is the recorded verdict for every provider in this
// repository that declares a copy on write value.
//
// Keyed by the provider's Name(), which is what a manifest refers to and what
// the comparison table's rows are labelled with.
var CopyOnWriteLedger = map[string]LedgerEntry{
	"pgurl": {
		Declared: false,
		Verdict:  Proved,
		Because: "a branch is a server side file copy and the suite measures it as one. " +
			"The false side of the assertion requires branch time to GROW with the data, " +
			"which is the side a copying provider on a copying harness can settle, and " +
			"the published benchmark shows 0.2 seconds at 8 MB against 25 to 110 seconds " +
			"at 1.43 GB.",
		Evidence: "engine/internal/db/pgurl/conformance_test.go",
	},
	"docker": {
		Declared: true,
		Verdict:  Unproven,
		Because: "no run in this repository records a verdict for this provider. The " +
			"declaration is about the daemon's storage driver rather than about anything " +
			"the provider does, the suite's own conformance test needs a live Docker " +
			"daemon, and the behaviour that would settle it builds two goldens as " +
			"committed images. An instrument that exists and has not been fired has " +
			"proved nothing.",
		Evidence: "engine/internal/db/docker/conformance_test.go",
	},
	"neon": {
		Declared: true,
		Verdict:  Unproven,
		Because: "no run in this repository records a verdict for this provider. Its " +
			"conformance test runs against the real Neon API, which is a harness that CAN " +
			"exhibit shared storage, so this is not the copying harness case and it is " +
			"settleable: it needs AF_NEON_API_KEY and AF_NEON_PROJECT_ID and somebody to " +
			"run it. Until then the declaration is Neon's claim rather than our finding.",
		Evidence: "engine/internal/db/neon/conformance_test.go",
	},
	"dblab": {
		Declared: true,
		Verdict:  Unproven,
		Because: "no run in this repository records a verdict for this provider. Its " +
			"conformance test runs against a real Database Lab Engine, which is a harness " +
			"that CAN exhibit shared storage, so it is settleable by anybody who stands " +
			"one up: it needs AF_DBLAB_URL and AF_DBLAB_TOKEN. The engine is self hosted " +
			"and costs nothing but disk, which makes this the cheapest of these to close.",
		Evidence: "engine/internal/db/dblab/conformance_test.go",
	},
	"supabase": {
		Declared: false,
		Verdict:  Unproven,
		Because: "no run in this repository records a verdict for this provider. A " +
			"Supabase branch is created empty and then loaded, so the declaration is " +
			"expected to hold, and expecting is what this ledger exists to stop being " +
			"mistaken for measuring. It needs a project on a paid plan.",
		Evidence: "engine/internal/db/supabase/conformance_test.go",
	},
}

// PublishableClaim renders a provider's copy on write declaration for a
// customer facing surface, from the ledger.
//
// A provider with no entry publishes as unproven rather than falling back to
// its declaration, for the reason every default in this package takes the
// cautious side: the failure mode of the missing case is a claim nobody
// checked appearing beside claims somebody did.
func PublishableClaim(providerName string) string {
	e, ok := CopyOnWriteLedger[providerName]
	if !ok {
		return notProved
	}
	return CopyOnWriteClaim(e.Declared, e.Verdict)
}
