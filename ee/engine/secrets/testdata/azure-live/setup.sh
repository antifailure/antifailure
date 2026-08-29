#!/usr/bin/env bash
#
# Provision a throwaway Azure Key Vault for TestAzure_Live_Conformance.
#
# Everything else this package knows about Key Vault is checked against a local
# server speaking the documented wire format. That proves what the adapter does
# with each response and proves nothing about whether Azure accepts the request,
# and the difference is not academic: the first live run found that Reach only
# acquired an Entra token and never touched the vault, so a vault behind a
# firewall or a typo reported itself perfectly usable. A one-process fake could
# not find that, because it serves the token endpoint and the vault together.
#
# WHAT IT CREATES, all of it disposable and none of it near anything else:
#   resource group  af-ee-secrets-test          (eastus)
#   key vault       af-ee-secrets-<random>      (standard, RBAC authorization)
#   secret          AF-LIVE-TOKEN               (a value generated here, now)
#   secret          AF-LIVE-EMPTY               (the empty string)
#   principal       af-ee-secrets-test          Key Vault Secrets User, that vault only
#   principal       af-ee-secrets-denied        Reader on the group, nothing on the data plane
#
# The second principal is the point of the exercise rather than an afterthought.
# It authenticates perfectly and can read nothing, which is the shape of a
# credential whose permissions changed underneath a running process, and it is
# the only way to tell "renewed and still refused" from "could not renew".
#
# COST: the standard tier has no monthly charge and operations are about $0.03
# per 10,000. A full run of the suite is a few dozen. Delete it all with:
#
#   az group delete -n af-ee-secrets-test --yes
#   az ad sp delete --id "$(az ad sp list --display-name af-ee-secrets-test --query '[0].id' -o tsv)"
#   az ad sp delete --id "$(az ad sp list --display-name af-ee-secrets-denied --query '[0].id' -o tsv)"
#
# CREDENTIALS NEVER ENTER THE REPOSITORY. They are written to a directory
# outside it, mode 700, with each file 600, and the test is given the directory
# rather than the values so that no client secret reaches a command line, the
# shell's history, or the process table.

set -euo pipefail

CRED_DIR="${AF_AZURE_LIVE_DIR:-$HOME/.af-secrets-live}"
RG="af-ee-secrets-test"
LOCATION="eastus"

command -v az >/dev/null || { echo "the Azure CLI is not installed" >&2; exit 1; }
az account show >/dev/null 2>&1 || { echo "run 'az login' first" >&2; exit 1; }

umask 077
mkdir -p "$CRED_DIR"

VAULT="af-ee-secrets-$(openssl rand -hex 3)"
printf '%s' "$VAULT" > "$CRED_DIR/vault-name"

echo "resource group $RG"
az group create -n "$RG" -l "$LOCATION" --query 'properties.provisioningState' -o tsv

echo "key vault $VAULT"
az keyvault create -n "$VAULT" -g "$RG" -l "$LOCATION" \
  --enable-rbac-authorization true --sku standard \
  --query 'properties.vaultUri' -o tsv

SCOPE=$(az keyvault show -n "$VAULT" -g "$RG" --query id -o tsv)
RGID=$(az group show -n "$RG" --query id -o tsv)

# Write access for whoever is running this, so the two fixtures can be created.
# Nothing reads with this identity; the test authenticates as the principal.
az role assignment create \
  --assignee-object-id "$(az ad signed-in-user show --query id -o tsv)" \
  --assignee-principal-type User \
  --role "Key Vault Secrets Officer" --scope "$SCOPE" -o none

echo "waiting for the role assignment to propagate"
VALUE="af-live-$(openssl rand -hex 16)"
printf '%s' "$VALUE" > "$CRED_DIR/azure-present-value"
for attempt in 1 2 3 4 5 6 7 8; do
  if az keyvault secret set --vault-name "$VAULT" -n AF-LIVE-TOKEN --value "$VALUE" -o none 2>/dev/null; then
    echo "AF-LIVE-TOKEN written on attempt $attempt"
    break
  fi
  [ "$attempt" = 8 ] && { echo "the role assignment never propagated" >&2; exit 1; }
  sleep 20
done

# Key Vault DOES store an empty value. The CLI refuses to send one, which is a
# property of the CLI and not of the service, and believing it would have
# skipped a behaviour against a store that supports it. Straight to the data
# plane, which answers 200 and reads back empty.
echo "AF-LIVE-EMPTY, over REST because the CLI will not send an empty value"
curl -fsS -X PUT "https://$VAULT.vault.azure.net/secrets/AF-LIVE-EMPTY?api-version=7.4" \
  -H "Authorization: Bearer $(az account get-access-token --resource https://vault.azure.net --query accessToken -o tsv)" \
  -H "Content-Type: application/json" -d '{"value":""}' -o /dev/null

# AF_LIVE_MISSING is deliberately never created. It is the absent variable.

echo "service principal, read only on that one vault"
az ad sp create-for-rbac --name "af-ee-secrets-test" \
  --role "Key Vault Secrets User" --scopes "$SCOPE" -o json > "$CRED_DIR/azure-sp.json"

echo "service principal, authenticates and can read nothing"
az ad sp create-for-rbac --name "af-ee-secrets-denied" \
  --role "Reader" --scopes "$RGID" -o json > "$CRED_DIR/azure-sp-denied.json"

chmod 600 "$CRED_DIR"/*
echo
echo "done. run the suite with:"
echo
echo "  AF_AZURE_LIVE_DIR=$CRED_DIR go test ./ee/engine/secrets/ -run Azure_Live -v"
