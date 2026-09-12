#!/usr/bin/env python3
"""Mutation table for lane L2.7.

One break per assertion. Each cell breaks exactly one production line, runs the
one test that is supposed to catch it, and is classified on the `=== RUN` count
rather than on the exit code: a break that stops the package compiling exits non
zero having run nothing, which is COULD-NOT-LOOK rather than a catch, and a -run
pattern matching nothing exits 0 and reads exactly like a pass.

Restore is from a file snapshot taken here, never from git, because the worktree
is shared state and a mutation leaves the state its mutation created.
"""
import os
import re
import shutil
import subprocess
import sys

ROOT = "/private/tmp/af-db-managed-mit"
ENGINE = os.path.join(ROOT, "engine")
SNAP = os.path.join(ROOT, ".lane-snap")
PG = "postgres://postgres@127.0.0.1:55731/postgres"

V = "engine/internal/db/managed/vendors.go"
P = "engine/internal/db/pgurl/pgurl.go"
S = "engine/internal/cli/start.go"
D = "docs/src/content/docs/providers/managed-postgres.md"
X = "engine/internal/db/xata/xata.go"
C = "engine/internal/db/xata/client.go"
FILES = [V, P, S, D, X, C]

# (cell name, file, old, new, package, test name)
CELLS = [
    ("registry covers the plan's thirteen", V,
     '\t{\n\t\tName:         "tembo",',
     '\t{\n\t\tName:         "tembo-DELETED-BY-MUTATION",',
     "./internal/db/managed/", "TestTheRegistryCoversThePlansThirteen"),

    ("Recognize matches on a label boundary", V,
     'if host == suffix || strings.HasSuffix(host, "."+suffix) {',
     'if strings.HasSuffix(host, suffix) {',
     "./internal/db/managed/", "TestRecognizeMatchesOnALabelBoundary"),

    ("Recognize strips the port", V,
     '\tif i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host, "]") {\n\t\thost = host[:i]\n\t}',
     '\tif false {\n\t\thost = host\n\t}',
     "./internal/db/managed/", "TestRecognizeMatchesOnALabelBoundary"),

    ("Heroku carries no host suffix", V,
     '\t\tName:    "heroku",',
     '\t\tName:    "heroku",\n\t\tHosts:   []string{"compute-1.amazonaws.com"},',
     "./internal/db/managed/", "TestHerokuIsNotRecognisedFromItsHost"),

    ("copy on write only with a CoW mechanism", V,
     '\t\tName:    "railway",\n\t\tDisplay: "Railway Postgres",',
     '\t\tName:        "railway",\n\t\tDisplay:     "Railway Postgres",\n\t\tCopyOnWrite: true,',
     "./internal/db/managed/", "TestCopyOnWriteIsDeclaredOnlyWithACopyOnWriteMechanism"),

    ("a yes or no verdict carries a quote", V,
     '\t\t\t\tQuote: "Heroku Postgres does not provide a superuser role for your " +\n'
     '\t\t\t\t\t"database, for the security and stability of all customers. Every " +\n'
     '\t\t\t\t\t"newly provisioned database includes a default credential named " +\n'
     '\t\t\t\t\t"owner, a permissive role one step below superuser.",',
     '\t\t\t\tQuote: "",',
     "./internal/db/managed/", "TestEveryVerdictCarriesItsEvidence"),

    ("an absent host suffix records why", V,
     '\t\tName:         "xata",\n\t\tDisplay:      "Xata",\n\t\tHostsUnknown: "no connection string example was found in the published documentation at the date it was read",',
     '\t\tName:    "xata",\n\t\tDisplay: "Xata",',
     "./internal/db/managed/", "TestAHostIsRecordedOrItsAbsenceIs"),

    ("no vendor suffix swallows another", V,
     '\t\tHosts:   []string{"proxy.rlwy.net", "railway.internal"},',
     '\t\tHosts:   []string{"proxy.rlwy.net", "railway.internal", "pg.aivencloud.com"},',
     "./internal/db/managed/", "TestNoVendorSuffixSwallowsAnother"),

    ("the refusal names the vendor", P,
     '\tif v, ok := managed.Recognize(hostport); ok && v.HostServer.Answer == managed.No {',
     '\tif v, ok := managed.Recognize(hostport); false && ok && v.HostServer.Answer == managed.No {',
     "./internal/db/pgurl/", "TestARefusalNamesTheVendorWhenTheGrantIsImpossible"),

    ("an unrecognised server keeps ALTER ROLE", P,
     '\tif v, ok := managed.Recognize(hostport); ok && v.HostServer.Answer == managed.No {',
     '\tif v, _ := managed.Recognize(hostport); true {',
     "./internal/db/pgurl/", "TestARefusalOnAnUnrecognisedServerKeepsTheGeneralRemedy"),

    ("an unverified vendor is not refused as a no", P,
     '\tif v, ok := managed.Recognize(hostport); ok && v.HostServer.Answer == managed.No {',
     '\tif v, ok := managed.Recognize(hostport); ok {',
     "./internal/db/pgurl/", "TestAnUnverifiedVendorGetsTheGeneralRemedyRatherThanAGuess"),

    ("the role probe reaches the refusal", P,
     '\tif !mayCreate {',
     '\tif false && !mayCreate {',
     "./internal/db/pgurl/", "TestTheRoleProbeReachesTheVendorAwareRefusal"),

    ("af start blocks on a documented no", S,
     '\t\t\tif v.HostServer.Answer == managed.No {\n\t\t\t\ts.state = StageBlocked',
     '\t\t\tif false && v.HostServer.Answer == managed.No {\n\t\t\t\ts.state = StageBlocked',
     "./internal/cli/", "TestStart_PgURLBlocksOnAVendorThatDocumentsItCannotHostTheGoldens"),

    ("af start does not block a vendor that can host", S,
     '\t\t\tif v.HostServer.Answer == managed.No {\n\t\t\t\ts.state = StageBlocked',
     '\t\t\tif v.HostServer.Answer != "" {\n\t\t\t\ts.state = StageBlocked',
     "./internal/cli/", "TestStart_PgURLNamesAVendorItCanHostOnWithoutBlocking"),

    ("an unrecognised host keeps its old line", S,
     '\tif v, ok := managed.Recognize(host); ok {\n\t\treturn host + ", which is " + v.Display\n\t}',
     '\tif v, _ := managed.Recognize(host); true {\n\t\treturn host + ", which is " + v.Display\n\t}',
     "./internal/cli/", "TestStart_PgURLOnAnUnrecognisedServerReadsExactlyAsItDidBefore"),

    ("the appended line names the vendor", S,
     '\tif v, ok := managed.Recognize(host); ok {\n\t\treturn host + ", which is " + v.Display\n\t}\n\treturn host',
     '\treturn host',
     "./internal/cli/", "TestStart_PgURLNamesAVendorItCanHostOnWithoutBlocking"),

    ("the page carries every vendor", D,
     '| Nile | none documented | no | unverified | [thenile.dev](https://thenile.dev/docs/support/backup_restore) |\n',
     '',
     "./internal/db/managed/", "TestTheDocumentedTableCarriesEveryVendorInTheRegistry"),

    ("the page's copy on write column", D,
     '| Xata | **copy on write branch** | **yes** |',
     '| Xata | **copy on write branch** | no |',
     "./internal/db/managed/", "TestTheDocumentedCopyOnWriteColumnMatchesTheRegistry"),

    ("the page's host server column", D,
     '| Heroku Postgres | fork restored from a snapshot | no | **no**, one database per add on and no superuser |',
     '| Heroku Postgres | fork restored from a snapshot | no | yes, one database per add on and no superuser |',
     "./internal/db/managed/", "TestTheDocumentedHostServerColumnMatchesTheRegistry"),

    ("xata create sends mode inherit", C,
     '\t\tMode:        "inherit",',
     '\t\tMode:        "custom",',
     "./internal/db/xata/", "TestTheRequestShapesAreTheOnesXataDocuments"),

    ("xata sends the key as a bearer token", C,
     '\treq.Header.Set("Authorization", "Bearer "+c.Key.Reveal())',
     '\treq.Header.Set("X-Api-Key", c.Key.Reveal())',
     "./internal/db/xata/", "TestTheRequestShapesAreTheOnesXataDocuments"),

    ("xata maps 412 to the coded ceiling", X,
     '\t\tif LimitExceeded(err) {',
     '\t\tif false && LimitExceeded(err) {',
     "./internal/db/xata/", "TestALimitRefusalBecomesTheCodedCeiling"),

    ("xata destroy tolerates a branch already gone", C,
     '\tif NotFound(err) {\n\t\treturn nil\n\t}\n\treturn err\n}',
     '\treturn err\n}',
     "./internal/db/xata/", "TestDestroyingABranchThatIsAlreadyGoneSucceeds"),

    ("xata refuses a golden with live branches", X,
     '\tif referencing > 0 {',
     '\tif false && referencing > 0 {',
     "./internal/db/xata/", "TestAGoldenWithLiveBranchesIsRefusedByCodeRatherThanByTheVendor"),

    ("xata says unverified rather than missing", X,
     '\t\tif _, unpublished := findCandidate(branches, version); unpublished {',
     '\t\tif _, unpublished := findCandidate(branches, version); false && unpublished {',
     "./internal/db/xata/", "TestBranchingAnUnpublishedCandidateSaysUnverifiedRatherThanMissing"),

    ("xata publishes only by the rename", X,
     '\tif err := p.client.RenameBranch(ctx, candidate.ID, PrefixGolden+branchSafe(version)); err != nil {',
     '\tif err := p.client.RenameBranch(ctx, candidate.ID, PrefixCandidate+branchSafe(version)); err != nil {',
     "./internal/db/xata/", "TestARefreshPublishesOnlyAfterVerificationAndABranchReadsTheData"),

    ("xata deletes the candidate when verification fails", X,
     '\t\tif !published {\n\t\t\t_ = p.client.DeleteBranch(context.WithoutCancel(ctx), candidate.ID)\n\t\t}',
     '\t\tif false && !published {\n\t\t\t_ = p.client.DeleteBranch(context.WithoutCancel(ctx), candidate.ID)\n\t\t}',
     "./internal/db/xata/", "TestAFailedVerificationLeavesNothingBranchable"),

    ("xata branch is idempotent by environment", X,
     '\t\tif b.Name == want {\n\t\t\treturn provider.Branch{',
     '\t\tif false && b.Name == want {\n\t\t\treturn provider.Branch{',
     "./internal/db/xata/", "TestBranchIsIdempotentByEnvironment"),

    ("xata declares copy on write truthfully", X,
     '\t\tCopyOnWrite: true,',
     '\t\tCopyOnWrite: false,',
     "./internal/db/xata/", "TestCopyOnWriteIsDeclaredAndTheSuiteCannotYetCheckItHere"),

    ("xata refuses reset rather than faking it", X,
     '\treturn provider.ErrUnsupported\n}',
     '\treturn nil\n}',
     "./internal/db/xata/", "TestResetIsRefusedRatherThanFaked"),

    ("xata refuses a half addressed project", X,
     '\tif opts.OrgID == "" || opts.ProjectID == "" {',
     '\tif false && (opts.OrgID == "" || opts.ProjectID == "") {',
     "./internal/db/xata/", "TestNewRefusesAKeylessOrHalfAddressedProject"),
]


def snapshot():
    os.makedirs(SNAP, exist_ok=True)
    for f in FILES:
        shutil.copy2(os.path.join(ROOT, f), os.path.join(SNAP, f.replace("/", "_")))


def restore():
    for f in FILES:
        shutil.copy2(os.path.join(SNAP, f.replace("/", "_")), os.path.join(ROOT, f))


def run(pkg, test):
    env = dict(os.environ)
    env["AF_PGURL_ADMIN_URL"] = PG
    env["AF_REQUIRE_DATABASE"] = "1"
    p = subprocess.run(
        ["go", "test", pkg, "-count=1", "-v", "-run", "^" + test + "$"],
        cwd=ENGINE, env=env, capture_output=True, text=True, timeout=1800)
    out = p.stdout + p.stderr
    runs = len(re.findall(r"^=== RUN\s+" + re.escape(test) + r"\s*$", out, re.M))
    failed_aimed = re.search(r"^\s*--- FAIL:\s+" + re.escape(test) + r"\b", out, re.M) is not None
    other_fail = re.findall(r"^\s*--- FAIL:\s+(\S+)", out, re.M)
    return p.returncode, runs, failed_aimed, other_fail, out


def classify(code, runs, aimed, others):
    if runs == 0:
        return "COULD-NOT-LOOK, the aimed test never ran"
    if aimed:
        return "CAUGHT"
    if code != 0:
        return "DID NOT DISCRIMINATE, a different test went red: " + ",".join(others)
    return "DID NOT DISCRIMINATE, stayed green"


def main():
    snapshot()
    rows = []
    for name, f, old, new, pkg, test in CELLS:
        path = os.path.join(ROOT, f)
        src = open(path).read()
        if src.count(old) != 1:
            rows.append((name, test, "NOT APPLIED, the anchor matched %d times" % src.count(old)))
            print("!! %-48s ANCHOR %d" % (name, src.count(old)), flush=True)
            continue
        open(path, "w").write(src.replace(old, new, 1))
        try:
            code, runs, aimed, others, out = run(pkg, test)
        finally:
            restore()
        verdict = classify(code, runs, aimed, others)
        rows.append((name, test, verdict))
        print("   %-48s runs=%d exit=%d  %s" % (name, runs, code, verdict), flush=True)
        if "DID NOT" in verdict or "COULD-NOT" in verdict:
            print(out[-2500:], flush=True)

    print("\n\n===== MUTATION TABLE =====")
    for name, test, verdict in rows:
        print("| %s | %s | %s |" % (name, test, verdict))

    # The green baseline after every restore, so the table is a statement about
    # a tree that exists rather than about one the last restore half rebuilt.
    print("\n===== BASELINE AFTER RESTORE =====")
    for pkg in ["./internal/db/managed/", "./internal/db/pgurl/", "./internal/cli/"]:
        env = dict(os.environ)
        env["AF_PGURL_ADMIN_URL"] = PG
        env["AF_REQUIRE_DATABASE"] = "1"
        pat = "TestStart_PgURL" if "cli" in pkg else ("TestARefusal|TestAnUnverified|TestTheRoleProbe" if "pgurl" in pkg else ".")
        p = subprocess.run(["go", "test", pkg, "-count=1", "-run", pat],
                           cwd=ENGINE, env=env, capture_output=True, text=True, timeout=1800)
        print("%-28s exit=%d  %s" % (pkg, p.returncode, (p.stdout + p.stderr).strip().splitlines()[-1]))
    return 0


if __name__ == "__main__":
    sys.exit(main())
