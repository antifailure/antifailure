#!/usr/bin/env bash
#
# Provision a throwaway AWS Secrets Manager fixture for TestAWS_Live_Conformance.
#
# THIS SCRIPT HAS NEVER BEEN RUN. There is no AWS account on this machine at
# all, so nothing below has been executed even once, and no line of it should be
# read as tested. It is written from the documented behaviour of the CLI, and
# the first person with an account should expect to correct it. Saying that
# plainly is the point: a setup script nobody has run, described as though
# somebody had, is how "the suites are written and the setup is scripted" came
# to be believed about files that did not exist.
#
# Everything else this package knows about Secrets Manager is checked against a
# local server speaking the documented wire format. That proves what the adapter
# does with each response and proves nothing about whether AWS accepts the
# request, and the difference is not academic: the first live Key Vault run
# found that Reach only acquired an Entra token and never touched the vault, so
# a vault behind a firewall or a typo reported itself perfectly usable. This
# adapter had the same fault and worse, because on the environment credential
# path it made no network call whatsoever. That is fixed, and the arm of the
# suite that would have caught it is the unreachable one.
#
# WHAT IT CREATES, all of it disposable and none of it near anything else:
#   secret       AF_LIVE_TOKEN           (a value generated here, now)
#   secret       AF_LIVE_EMPTY           (the empty string, if the service takes one)
#   iam user     af-ee-secrets-test      GetSecretValue on those two secrets only
#   iam user     af-ee-secrets-denied    no permissions at all
#
# The second principal is the point of the exercise rather than an afterthought.
# It authenticates perfectly and can read nothing, which is the shape of a
# credential whose permissions changed underneath a running process, and it is
# the only way to tell "renewed and still refused" from "could not renew".
#
# AF_LIVE_MISSING is deliberately never created. It is the absent variable, and
# the behaviour that a miss falls through while a failure does not is the one
# the whole lookup chain rests on.
#
# WHETHER SECRETS MANAGER HOLDS AN EMPTY VALUE IS NOT KNOWN HERE. Key Vault does,
# which was settled by sending one and watching the service answer 200, and the
# az CLI's refusal to send one turned out to be a property of the CLI rather
# than of the service. Nobody has been able to ask AWS the same question. So
# this script ASKS IT and writes the answer to empty-supported, and the suite
# asserts the consequence either way rather than assuming one.
#
# COST: Secrets Manager charges per secret per month, prorated, plus a small
# per-API-call charge. Two secrets for the length of a test run round to
# nothing, but they are billed for as long as they exist, so delete them:
#
#   aws secretsmanager delete-secret --secret-id AF_LIVE_TOKEN --force-delete-without-recovery
#   aws secretsmanager delete-secret --secret-id AF_LIVE_EMPTY --force-delete-without-recovery
#   for u in af-ee-secrets-test af-ee-secrets-denied; do
#     for k in $(aws iam list-access-keys --user-name "$u" --query 'AccessKeyMetadata[].AccessKeyId' -o text); do
#       aws iam delete-access-key --user-name "$u" --access-key-id "$k"
#     done
#     aws iam delete-user-policy --user-name "$u" --policy-name af-ee-secrets-read 2>/dev/null || true
#     aws iam delete-user --user-name "$u"
#   done
#
# CREDENTIALS NEVER ENTER THE REPOSITORY. They are written to a directory
# outside it, mode 700, with each file 600, and the test is given the directory
# rather than the values so that no secret access key reaches a command line,
# the shell's history, or the process table. Nothing below echoes a key: the
# CLI writes them straight to a file.

set -euo pipefail

CRED_DIR="${AF_AWS_LIVE_DIR:-$HOME/.af-secrets-live-aws}"
REGION="${AWS_REGION:-us-east-1}"
READER="af-ee-secrets-test"
DENIED="af-ee-secrets-denied"

command -v aws >/dev/null || { echo "the AWS CLI is not installed" >&2; exit 1; }
aws sts get-caller-identity >/dev/null 2>&1 || {
  echo "no AWS credentials are configured: run 'aws configure' or export a key first" >&2
  exit 1
}

umask 077
mkdir -p "$CRED_DIR"

printf '%s' "$REGION" > "$CRED_DIR/region"
ACCOUNT=$(aws sts get-caller-identity --query Account --output text)

echo "secret AF_LIVE_TOKEN in $REGION"
VALUE="af-live-$(openssl rand -hex 16)"
printf '%s' "$VALUE" > "$CRED_DIR/aws-present-value"
# The value goes in on stdin rather than as an argument, so it is not in the
# process table while the call runs. file:// with a dash reads standard input.
printf '%s' "$VALUE" | aws secretsmanager create-secret \
  --name AF_LIVE_TOKEN --region "$REGION" \
  --secret-string file:///dev/stdin --output text --query Name

# The question nobody here has been able to answer. If the service takes an
# empty value the suite runs that behaviour; if it refuses, the suite skips
# exactly that one behaviour with the reason the conformance suite supplies,
# and fails on any other skip.
echo "secret AF_LIVE_EMPTY, which is the question this script exists to settle"
if printf '' | aws secretsmanager create-secret \
     --name AF_LIVE_EMPTY --region "$REGION" \
     --secret-string file:///dev/stdin --output text --query Name 2>/dev/null; then
  printf 'yes' > "$CRED_DIR/empty-supported"
  echo "Secrets Manager accepted an empty value"
else
  printf 'no' > "$CRED_DIR/empty-supported"
  echo "Secrets Manager refused an empty value, which the suite will record as a named skip"
fi

echo "iam user $READER, GetSecretValue on the AF_LIVE_ secrets only"
aws iam create-user --user-name "$READER" --output text --query 'User.UserName'
# Scoped by name prefix rather than to the whole service, so a key that leaks
# out of the credential directory cannot read anything else in the account.
#
# THE WILDCARD HAS TO COVER AF_LIVE_MISSING, which is the secret that does not
# exist, and getting that wrong would break the behaviour the whole lookup chain
# rests on. A principal WITH permission on a name that is absent is told
# ResourceNotFoundException, which the adapter reports as a miss and the chain
# falls through. A principal WITHOUT permission on that name is told
# AccessDeniedException instead, which the adapter correctly reports as a
# refused credential, and the suite would then fail "reports a name it does not
# hold as a miss, not a failure" while the adapter was behaving perfectly. The
# trailing wildcard also covers the six character suffix AWS appends to every
# secret ARN.
aws iam put-user-policy --user-name "$READER" --policy-name af-ee-secrets-read \
  --policy-document "$(cat <<JSON
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": "secretsmanager:GetSecretValue",
    "Resource": ["arn:aws:secretsmanager:$REGION:$ACCOUNT:secret:AF_LIVE_*"]
  }]
}
JSON
)"
aws iam create-access-key --user-name "$READER" --output json > "$CRED_DIR/aws-keys.json"

echo "iam user $DENIED, authenticates and can read nothing"
aws iam create-user --user-name "$DENIED" --output text --query 'User.UserName'
# No policy at all. The keys are valid, the signature verifies, and every call
# comes back AccessDeniedException, which is what the adapter must report as a
# refused credential rather than as a miss.
aws iam create-access-key --user-name "$DENIED" --output json > "$CRED_DIR/aws-keys-denied.json"

# IAM is eventually consistent and a new access key is not usable the instant it
# is returned. Waiting here rather than letting the suite fail on it, because a
# suite that fails for this reason looks exactly like an adapter that cannot
# authenticate.
echo "waiting for the new keys to become usable"
for attempt in 1 2 3 4 5 6 7 8; do
  # AWS_SESSION_TOKEN and AWS_PROFILE are cleared as well as the keys set. An
  # inherited session token belongs to the identity that ran this script, and
  # left in place it would be sent alongside the new key and the loop would be
  # proving the wrong principal usable.
  if AWS_ACCESS_KEY_ID=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["AccessKey"]["AccessKeyId"])' "$CRED_DIR/aws-keys.json") \
     AWS_SECRET_ACCESS_KEY=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["AccessKey"]["SecretAccessKey"])' "$CRED_DIR/aws-keys.json") \
     AWS_SESSION_TOKEN= AWS_PROFILE= \
     aws secretsmanager get-secret-value --secret-id AF_LIVE_TOKEN --region "$REGION" \
       --query Name --output text >/dev/null 2>&1; then
    echo "usable on attempt $attempt"
    break
  fi
  [ "$attempt" = 8 ] && { echo "the new keys never became usable" >&2; exit 1; }
  sleep 15
done

chmod 600 "$CRED_DIR"/*
echo
echo "done. account $ACCOUNT, region $REGION. run the suite with:"
echo
echo "  AF_AWS_LIVE_DIR=$CRED_DIR go test ./ee/engine/secrets/ -run AWS_Live -v"
