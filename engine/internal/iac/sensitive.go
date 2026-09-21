package iac

import (
	"strings"

	"github.com/antifailure/antifailure/engine/internal/redact"
)

// WHAT THIS FILE STOPS, and why one control was not enough.
//
// The brief for this reader is environment variable NAMES and never values,
// and secret REFERENCES and never secrets. That is easy to write and easy to
// get wrong in four different ways, so there are four independent controls and
// each one catches a case the others do not.
//
//  1. THE NAME SAYS SO. `administrator_login_password`, `connection_string`,
//     `secret_access_key`. Caught by credentialName below.
//  2. THE CONFIGURATION SAYS SO. A variable declared `sensitive = true` has
//     its value dropped at resolution time, in hcleval.go, so that it never
//     enters an evaluated value at all rather than being filtered out of one
//     later.
//  3. THE RESOURCE SAYS SO. `azurerm_key_vault_secret` has an attribute called
//     `value`, which no name based rule would ever catch, and it holds the
//     secret. Caught by secretBearing below, per resource type.
//  4. THE PLAN SAYS SO. `terraform show -json` marks sensitive attributes in a
//     parallel `sensitive_values` object, and that mask is honoured in plan.go.
//
// AND THEN THE SHAPE SAYS SO. Everything that survives all four goes through
// the engine's own redactor on its way into a Reading. That is the net for a
// credential written in line under a name nobody would guess, and it is the
// same instrument that guards every other thing this engine writes, rather
// than a second weaker copy of it.
//
// MEASURED, NOT ASSUMED, for control 4. Running Terraform v1.15.8 over a
// configuration with `variable "password" { sensitive = true }` feeding a
// resource, `terraform show -json` of the plan puts the literal secret in
// `resource_changes[0].change.after.input` AND in `planned_values` `values`,
// with `sensitive_values: {"input": true}` beside each as the only signal, and
// puts it a third time in the top level `variables` object with NO sensitivity
// marker at all. So a reader that took plan JSON at face value would publish
// the password three times. plan.go honours the mask and never reads the top
// level `variables` object.

// credentialWords are name segments that mean the value is a credential.
var credentialWords = map[string]bool{
	"password": true, "passwd": true, "pwd": true, "passphrase": true,
	"secret": true, "token": true, "credential": true, "credentials": true,
	"apikey": true, "privatekey": true, "signature": true, "sas": true,
}

// keyQualifiers are segments that turn a following `key` into a credential.
// `key` on its own is not one: `key_vault_id`, `partition_key` and `key_name`
// are all locators, and dropping them would cost a reader real information for
// no safety.
var keyQualifiers = map[string]bool{
	"private": true, "secret": true, "access": true, "api": true,
	"encryption": true, "signing": true, "shared": true, "primary": true,
	"secondary": true, "master": true, "account": true, "client": true,
}

// locatorSuffixes are final segments that make a name a pointer to a secret
// rather than the secret.
//
// This is the rule that keeps the reader USEFUL. `key_vault_secret_id` is the
// single most important attribute a container app has, because it says which
// secret a variable is fed from, and a blanket ban on the word `secret` would
// throw it away. It is also the rule that gets AWS exactly right by accident
// of naming: `access_key_id` is the public half and ends in `id`, while
// `secret_access_key` is the private half and does not.
var locatorSuffixes = map[string]bool{
	"id": true, "ids": true, "name": true, "names": true, "ref": true, "refs": true,
	"uri": true, "url": true, "arn": true, "endpoint": true, "version": true,
	"identifier": true,
}

// credentialName says whether an attribute of this name holds a credential.
//
// It reads the name as snake case segments rather than as a substring, so that
// `keystore_path` is not caught by `key` and `token_endpoint` is not caught by
// `token`.
func credentialName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	// A dotted attribute path is judged on its last element, which is the
	// attribute itself: `sku.name` is the sku's name, not a name under a sku.
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	if strings.Contains(name, "connection_string") || strings.Contains(name, "connstring") ||
		strings.Contains(name, "conn_string") {
		return true
	}
	segs := strings.FieldsFunc(name, func(r rune) bool { return r == '_' || r == '-' })
	if len(segs) == 0 {
		return false
	}
	if locatorSuffixes[segs[len(segs)-1]] {
		return false
	}
	for i, seg := range segs {
		if credentialWords[seg] {
			return true
		}
		if (seg == "key" || seg == "keys") && i > 0 && keyQualifiers[segs[i-1]] {
			return true
		}
	}
	return false
}

// secretBearing names, per resource type, the attributes that hold the secret
// itself.
//
// These are the ones no name based rule can catch, because the attribute is
// called `value` or `data`. A resource type whose whole purpose is to hold a
// secret gets its value bearing attributes dropped whatever they are called.
var secretBearing = map[string]map[string]bool{
	"azurerm_key_vault_secret":                {"value": true},
	"azurerm_key_vault_certificate":           {"certificate": true},
	"aws_secretsmanager_secret_version":       {"secret_string": true, "secret_binary": true},
	"aws_ssm_parameter":                       {"value": true},
	"google_secret_manager_secret_version":    {"secret_data": true},
	"kubernetes_secret":                       {"data": true, "string_data": true, "binary_data": true},
	"kubernetes_secret_v1":                    {"data": true, "string_data": true, "binary_data": true},
	"random_password":                         {"result": true, "bcrypt_hash": true},
	"random_id":                               {"b64_std": true, "b64_url": true, "hex": true, "dec": true},
	"random_string":                           {"result": true},
	"azurerm_container_registry_token_passwd": {"value": true},
	"tls_private_key":                         {"private_key_pem": true, "private_key_openssh": true, "private_key_pem_pkcs8": true},
}

// dropped is the reason that replaces a value this reader will not carry.
//
// It is a sentence in the same voice as every other unreadable reason, because
// to a caller "I will not carry this" and "I could not resolve this" are both
// holes and both need saying. They are distinguishable: this one names the
// control that dropped it.
const droppedByName = "the attribute's name says it holds a credential, so this reader records " +
	"that it is declared and not what it is"

const droppedByType = "this resource type holds a secret in this attribute, so this reader " +
	"records that it is declared and not what it is"

const droppedByShape = "the value has the shape of a credential, so this reader records that it " +
	"is declared and not what it is"

// scrub is the last gate every value passes on its way into a Reading.
//
// It takes the attribute's name and its resource type, so that all three name
// based controls are applied in ONE place: a caller cannot construct a
// Component with an unscrubbed attribute without going around this function,
// and every path that builds one goes through it.
func scrub(resourceType, name string, v Value[string]) Value[string] {
	if credentialName(name) {
		return Refused[string](droppedByName, v.At())
	}
	if attrs, ok := secretBearing[resourceType]; ok {
		leaf := name
		if i := strings.LastIndex(leaf, "."); i >= 0 {
			leaf = leaf[i+1:]
		}
		if attrs[leaf] {
			return Refused[string](droppedByType, v.At())
		}
	}
	got, ok := v.Get()
	if !ok {
		return v
	}
	if cleaned := redactor.String(got); cleaned != got {
		return Refused[string](droppedByShape, v.At())
	}
	return v
}

// redactor is the engine's own redactor, with its pattern rules.
//
// It is a package level value because constructing one compiles a set of
// regular expressions and an attribute is scrubbed once per attribute of every
// resource in a tree. It carries no registered exact secrets, since this
// package loads none: what it contributes here is the SHAPE rules, the ones
// that recognise a provider key prefix, a PEM block, a bearer token or a
// password inside a connection string without being told the value.
var redactor = redact.New()
