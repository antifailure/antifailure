#!/usr/bin/env bash
#
# Provision a throwaway Google Secret Manager fixture for TestGCP_Live_Conformance.
#
# THIS SCRIPT HAS NOT BEEN RUN END TO END, and it cannot be on this machine. It
# stops at the very first step, enabling the Secret Manager API, because that
# API refuses to enable on a project with no open billing account. Everything
# after that step is written from the documented behaviour of the CLI and has
# never been executed. Saying that plainly is the point: a setup script nobody
# has run, described as though somebody had, is how "the suites are written and
# the setup is scripted" came to be believed about files that did not exist.
#
# WHAT IS ACTUALLY TRUE ON THIS MACHINE, measured rather than remembered:
# virrsanghavi@gmail.com is authenticated in gcloud and a throwaway project
# exists. Enabling secretmanager.googleapis.com returns
# UREQ_PROJECT_BILLING_NOT_FOUND. The one billing account on the tenant reports
# open: false, so linking it leaves billingEnabled: false. That is a payment
# method, not work, and the check below names it rather than letting a raw API
# error stand in for it.
#
# Everything else this package knows about Secret Manager is checked against a
# local server speaking the documented wire format. That proves what the adapter
# does with each response and proves nothing about whether Google accepts the
# request, and the difference is not academic: the first live Key Vault run
# found that Reach only acquired an Entra token and never touched the vault, so
# a vault behind a firewall or a typo reported itself perfectly usable. This
# adapter had exactly the same fault, acquiring an OAuth token from
# oauth2.googleapis.com and never speaking to secretmanager.googleapis.com. That
# is fixed, and the arm of the suite that would have caught it is the
# unreachable one.
#
# WHAT IT CREATES, all of it disposable and none of it near anything else:
#   secret            AF_LIVE_TOKEN         (a value generated here, now)
#   secret            AF_LIVE_EMPTY         (the empty payload, if the service takes one)
#   service account   af-ee-secrets-test    secretAccessor on the AF_LIVE_ secrets
#   service account   af-ee-secrets-denied  no role at all
#
# The second principal is the point of the exercise rather than an afterthought.
# It authenticates perfectly and can read nothing, which is the shape of a
# credential whose permissions changed underneath a running process, and it is
# the only way to tell "renewed and still refused" from "could not renew".
#
# AF_LIVE_MISSING is deliberately never created. It is the absent variable, and
# the behaviour that a miss falls through while a failure does not is the one
# the whole lookup chain rests on. The reader is granted its role at PROJECT
# level rather than per secret for that reason: a principal without permission
# on an absent name is told PERMISSION_DENIED rather than NOT_FOUND, which the
# adapter correctly reports as a refused credential, and the suite would then
# fail "reports a name it does not hold as a miss" while the adapter was
# behaving perfectly.
#
# WHETHER SECRET MANAGER HOLDS AN EMPTY PAYLOAD IS NOT KNOWN HERE. Key Vault
# does, which was settled by sending one and watching the service answer 200,
# and the az CLI's refusal to send one turned out to be a property of the CLI
# rather than of the service. Nobody has been able to ask Google the same
# question. So this script ASKS IT and writes the answer to empty-supported, and
# the suite asserts the consequence either way rather than assuming one.
#
# COST: Secret Manager charges per secret version per month and per access
# operation, both small. Two secrets for the length of a test run round to
# nothing, but they are billed while they exist, so delete them:
#
#   gcloud secrets delete AF_LIVE_TOKEN --project "$PROJECT" --quiet
#   gcloud secrets delete AF_LIVE_EMPTY --project "$PROJECT" --quiet
#   for s in af-ee-secrets-test af-ee-secrets-denied; do
#     gcloud iam service-accounts delete "$s@$PROJECT.iam.gserviceaccount.com" \
#       --project "$PROJECT" --quiet
#   done
#
# CREDENTIALS NEVER ENTER THE REPOSITORY. They are written to a directory
# outside it, mode 700, with each file 600, and the test is given the directory
# rather than the values so that no service account key reaches a command line,
# the shell's history, or the process table. Nothing below echoes a key: gcloud
# writes them straight to a file.

set -euo pipefail

CRED_DIR="${AF_GCP_LIVE_DIR:-$HOME/.af-secrets-live-gcp}"
PROJECT="${GOOGLE_CLOUD_PROJECT:-$(gcloud config get-value project 2>/dev/null)}"
READER="af-ee-secrets-test"
DENIED="af-ee-secrets-denied"

command -v gcloud >/dev/null || { echo "the Google Cloud CLI is not installed" >&2; exit 1; }
gcloud auth list --filter=status:ACTIVE --format='value(account)' | grep -q . || {
  echo "run 'gcloud auth login' first" >&2; exit 1
}
[ -n "$PROJECT" ] || { echo "no project: set GOOGLE_CLOUD_PROJECT or 'gcloud config set project'" >&2; exit 1; }

umask 077
mkdir -p "$CRED_DIR"
printf '%s' "$PROJECT" > "$CRED_DIR/project"

# THE STEP THAT STOPS TODAY.
#
# Reported as the billing problem it is rather than as the API error it arrives
# as. UREQ_PROJECT_BILLING_NOT_FOUND on its own reads like a missing project or
# a permissions fault, and somebody who has not seen it before will go and check
# both. The message below says what is actually required, so the next person
# spends their time on a payment method rather than on a diagnosis that has
# already been done twice.
echo "enabling secretmanager.googleapis.com on $PROJECT"
if ! ENABLE_ERROR=$(gcloud services enable secretmanager.googleapis.com \
     --project "$PROJECT" 2>&1); then
  echo >&2
  echo "could not enable the Secret Manager API on $PROJECT." >&2
  case "$ENABLE_ERROR" in
    *BILLING*|*billing*)
      cat >&2 <<'WHY'
This is a billing problem and not a permissions problem. Secret Manager will
not enable on a project with no OPEN billing account attached, and on this
tenant the only billing account reports open: false, so linking it leaves the
project with billingEnabled: false. Confirm with:

  gcloud billing accounts list
  gcloud beta billing projects describe "$PROJECT"

An account showing OPEN: False needs a valid payment method added in the
console before anything here can proceed. Nothing in this repository can work
around it, and the suite it blocks is TestGCP_Live_Conformance, which skips
cleanly while AF_GCP_LIVE_DIR is unset.
WHY
      ;;
    *)
      echo "the API returned:" >&2
      echo "$ENABLE_ERROR" >&2
      ;;
  esac
  exit 1
fi

echo "secret AF_LIVE_TOKEN"
VALUE="af-live-$(openssl rand -hex 16)"
printf '%s' "$VALUE" > "$CRED_DIR/gcp-present-value"
# The value goes in on stdin rather than as an argument, so it is not in the
# process table while the call runs.
printf '%s' "$VALUE" | gcloud secrets create AF_LIVE_TOKEN \
  --project "$PROJECT" --replication-policy automatic --data-file=- --quiet

# The question nobody here has been able to answer. If the service takes an
# empty payload the suite runs that behaviour; if it refuses, the suite skips
# exactly that one behaviour with the reason the conformance suite supplies,
# and fails on any other skip.
echo "secret AF_LIVE_EMPTY, which is the question this script exists to settle"
if printf '' | gcloud secrets create AF_LIVE_EMPTY \
     --project "$PROJECT" --replication-policy automatic --data-file=- --quiet 2>/dev/null; then
  printf 'yes' > "$CRED_DIR/empty-supported"
  echo "Secret Manager accepted an empty payload"
else
  printf 'no' > "$CRED_DIR/empty-supported"
  echo "Secret Manager refused an empty payload, which the suite will record as a named skip"
fi

echo "service account $READER, able to read secret values"
gcloud iam service-accounts create "$READER" --project "$PROJECT" \
  --display-name "Antifailure enterprise secrets conformance" --quiet
gcloud projects add-iam-policy-binding "$PROJECT" \
  --member "serviceAccount:$READER@$PROJECT.iam.gserviceaccount.com" \
  --role roles/secretmanager.secretAccessor --condition None --quiet >/dev/null
gcloud iam service-accounts keys create "$CRED_DIR/gcp-sa.json" \
  --iam-account "$READER@$PROJECT.iam.gserviceaccount.com" --project "$PROJECT" --quiet

echo "service account $DENIED, authenticates and can read nothing"
gcloud iam service-accounts create "$DENIED" --project "$PROJECT" \
  --display-name "Antifailure enterprise secrets conformance, denied" --quiet
# No binding at all. The key is valid, the assertion is accepted, a token comes
# back, and every access comes back PERMISSION_DENIED, which is what the adapter
# must report as a refused credential rather than as a miss.
gcloud iam service-accounts keys create "$CRED_DIR/gcp-sa-denied.json" \
  --iam-account "$DENIED@$PROJECT.iam.gserviceaccount.com" --project "$PROJECT" --quiet

# IAM is eventually consistent and a new binding is not in force the instant it
# returns. Waiting here rather than letting the suite fail on it, because a
# suite that fails for this reason looks exactly like an adapter that cannot
# authenticate.
echo "waiting for the binding to propagate"
for attempt in 1 2 3 4 5 6 7 8; do
  if GOOGLE_APPLICATION_CREDENTIALS="$CRED_DIR/gcp-sa.json" \
     gcloud secrets versions access latest --secret AF_LIVE_TOKEN \
       --project "$PROJECT" --quiet >/dev/null 2>&1; then
    echo "in force on attempt $attempt"
    break
  fi
  [ "$attempt" = 8 ] && { echo "the binding never propagated" >&2; exit 1; }
  sleep 15
done

chmod 600 "$CRED_DIR"/*
echo
echo "done. project $PROJECT. run the suite with:"
echo
echo "  AF_GCP_LIVE_DIR=$CRED_DIR go test ./ee/engine/secrets/ -run GCP_Live -v"
