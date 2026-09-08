package detect

import (
	"fmt"
	"sort"
	"strings"
)

// The clouds, recognised from the SDK a repository actually depends on.
//
// The catalog matches a dependency by its exact name, which works for a
// package called stripe and stops working the moment a vendor ships one client
// per service. @aws-sdk/client-lambda, @google-cloud/bigquery and
// @azure/identity were each a dependency no entry claimed, so the application
// got no rule for the host it was about to call, and the request was refused
// with "no rule matches" rather than with the service's name. That is the same
// defect L0.3 fixed for the nine AWS services it named, one level up: the
// wildcard is gone and the gap it hid is now a silence.
//
// A silence is what this file turns into a sentence. Every cloud SDK package
// is parsed into the cloud it belongs to and the service token it names, the
// tokens the catalog claims are subtracted, and what is left is REPORTED by
// name rather than left to be discovered when something is refused in
// production shaped code.

// cloudDisplayName is what a person calls the cloud.
func cloudDisplayName(cloud string) string {
	switch cloud {
	case "aws":
		return "AWS"
	case "gcp":
		return "Google Cloud"
	case "azure":
		return "Azure"
	}
	return cloud
}

// cloudSDK is one dependency recognised as a cloud client.
type cloudSDK struct {
	// Cloud is aws, gcp or azure.
	Cloud string
	// Token is the service the package names, in the vendor's own spelling.
	// Empty for an SDK that covers the whole cloud in one package, which boto3
	// and the v2 aws-sdk both are.
	Token string
	// Package is the dependency as declared.
	Package string
}

// cloudSDKOf parses a dependency name into the cloud and service it names.
//
// The vendor's own package layout is the parser: the AWS JavaScript SDK v3
// ships one @aws-sdk/client-<service> per service, Google ships
// @google-cloud/<service>, and Azure ships @azure/<service>. Python spells the
// same three as boto3, google-cloud-<service> and azure-<service>.
//
// It returns the token and NOT a host, which is the whole point. Turning
// cloudwatch-logs into logs.<region>.amazonaws.com, sfn into states, or
// secret-manager into secretmanager is a table, and it lives in the catalog
// where each entry also carries the reason it is refused. A host derived here
// mechanically would be wrong for those three and silently wrong, which is
// worse than not naming the service at all.
func cloudSDKOf(pkg string) (cloudSDK, bool) {
	p := strings.ToLower(pkg)
	switch {
	case strings.HasPrefix(p, "@aws-sdk/client-"):
		return cloudSDK{Cloud: "aws", Token: strings.TrimPrefix(p, "@aws-sdk/client-"), Package: pkg}, true
	case strings.HasPrefix(p, "@aws-sdk/"):
		// lib-storage, credential-providers, s3-request-presigner and the
		// rest are helpers around a client rather than services of their own.
		// The cloud is established; the service is not, and claiming one
		// would invent a host.
		return cloudSDK{Cloud: "aws", Package: pkg}, true
	case strings.HasPrefix(p, "@google-cloud/"):
		return cloudSDK{Cloud: "gcp", Token: strings.TrimPrefix(p, "@google-cloud/"), Package: pkg}, true
	case strings.HasPrefix(p, "@azure/"):
		return cloudSDK{Cloud: "azure", Token: strings.TrimPrefix(p, "@azure/"), Package: pkg}, true
	case strings.HasPrefix(p, "google-cloud-"):
		return cloudSDK{Cloud: "gcp", Token: strings.TrimPrefix(p, "google-cloud-"), Package: pkg}, true
	case strings.HasPrefix(p, "azure-"):
		return cloudSDK{Cloud: "azure", Token: strings.TrimPrefix(p, "azure-"), Package: pkg}, true
	case p == "boto3", p == "botocore", p == "aioboto3", p == "aiobotocore",
		p == "aws-sdk", p == "aws-sdk-go", p == "aws-sdk-go-v2":
		// One package for the whole of AWS. It says which cloud and nothing
		// about which service, and guessing from it is what put every AWS
		// host under a mail rule in the first place.
		return cloudSDK{Cloud: "aws", Package: pkg}, true
	}
	return cloudSDK{}, false
}

// catalogClaimsToken reports whether an entry covers a service token.
func (tp ThirdParty) catalogClaimsToken(cloud, token string) bool {
	if tp.Cloud != cloud || token == "" {
		return false
	}
	for _, t := range tp.Tokens {
		if t == token {
			return true
		}
	}
	return false
}

// thirdPartiesForCloud returns the catalog entries for a cloud.
//
// With tokens given, only the entries those tokens name, which is what an
// emulator's own configuration provides: LocalStack's SERVICES=s3,sqs,sns is
// the developer stating which three services this application uses, and it is
// better evidence than a dependency list because it is what they configured
// rather than what they installed. With no tokens, nothing: a cloud named with
// no service named is exactly the wildcard shape this catalog was rewritten to
// remove, and the caller reports the gap instead.
func thirdPartiesForCloud(cloud string, tokens []string) []ThirdParty {
	if len(tokens) == 0 {
		return nil
	}
	var out []ThirdParty
	for _, tp := range thirdParties {
		for _, tok := range tokens {
			if tp.catalogClaimsToken(cloud, strings.ToLower(strings.TrimSpace(tok))) {
				out = append(out, tp)
				break
			}
		}
	}
	return out
}

// unnamedCloudServices reports the cloud SDK packages no catalog entry claims.
//
// This is the instrument that can say no. A catalog with a gap in it and a
// catalog with no gap look identical from the outside, and the difference only
// shows up as a refusal nobody can read, in an environment, at the moment the
// application makes the call. Naming the gap at af init is the whole point:
// the finding says which package, which cloud and which service, so the answer
// is a pull request against the catalog rather than a bug report about a
// refusal.
func unnamedCloudServices(deps map[string]string) []Finding {
	type gap struct {
		cloud, token, pkg, evidence string
	}
	seen := map[string]bool{}
	var gaps []gap
	for pkg, file := range deps {
		sdk, ok := cloudSDKOf(pkg)
		if !ok || sdk.Token == "" {
			// A package naming the cloud and not the service cannot produce a
			// host, so there is no gap to report: the services it reaches are
			// whatever the rest of the dependency list says.
			continue
		}
		claimed := false
		for _, tp := range thirdParties {
			if tp.catalogClaimsToken(sdk.Cloud, sdk.Token) {
				claimed = true
				break
			}
		}
		if claimed || seen[sdk.Cloud+"/"+sdk.Token] {
			continue
		}
		seen[sdk.Cloud+"/"+sdk.Token] = true
		gaps = append(gaps, gap{cloud: sdk.Cloud, token: sdk.Token, pkg: pkg, evidence: file})
	}
	// deps iterates in a random order and these findings reach the manifest's
	// reader, so sort them or two runs over one tree disagree.
	sort.Slice(gaps, func(i, j int) bool {
		if gaps[i].cloud != gaps[j].cloud {
			return gaps[i].cloud < gaps[j].cloud
		}
		return gaps[i].token < gaps[j].token
	})

	out := make([]Finding, 0, len(gaps))
	for _, g := range gaps {
		out = append(out, Finding{
			Kind: KindNote, Subject: "cloud-service." + g.cloud + "." + g.token,
			Value: g.pkg, Confidence: High, Evidence: g.evidence,
			Analyzer: "thirdparty",
			Detail: fmt.Sprintf(
				"%s depends on %s, so this application calls the %s service %s, and the catalog "+
					"has no host for it. Requests to it are refused, because the default is block, "+
					"and the refusal says no rule matches rather than naming the service. Add an "+
					"entry for it, or write the rule into egress by hand.",
				g.evidence, g.pkg, cloudDisplayName(g.cloud), g.token),
		})
	}
	return out
}

// DetectedEmulator is a cloud emulator the repository already runs in its own
// compose file, and what detection did about it.
//
// Every field on it exists to be printed. An emulator is the strongest
// statement a repository makes about which cloud it talks to, and it is also
// the thing this product replaces, so a person reading af init has to be told
// that it was found, which services it named, which rules that produced, and
// which services it named that got no rule at all. The last of those is the
// one that would otherwise be invisible: a service with no rule is refused
// with "no rule matches", in an environment, at the moment it is called.
type DetectedEmulator struct {
	// Product is what the emulator is called: LocalStack, Azurite.
	Product string
	// Cloud is aws, gcp or azure.
	Cloud string
	// Service is the compose service that runs it.
	Service string
	// Image is the image as the compose file declares it.
	Image string
	// Evidence is the file the emulator was found in.
	Evidence string
	// Services are the service tokens the emulator's own configuration names,
	// in the developer's own spelling. Empty when it names none.
	Services []string
	// Hosts are the egress rules this emulator produced, by host, and empty
	// for an emulator that named no service the catalog claims.
	Hosts []string
	// Unnamed are the tokens the emulator named that no catalog entry claims.
	// Each one is a service this application uses and has no rule for.
	Unnamed []string
}

// emulatorTokens splits an emulator's own service list into tokens.
//
// LocalStack spells it SERVICES=s3,sqs,sns, and older compose files put spaces
// after the commas. Anything else that carries a list carries it the same way.
func emulatorTokens(list string) []string {
	var out []string
	seen := map[string]bool{}
	for _, tok := range strings.Split(list, ",") {
		tok = strings.ToLower(strings.TrimSpace(tok))
		if tok == "" || seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	sort.Strings(out)
	return out
}

// unclaimedTokens returns the tokens no catalog entry claims for a cloud.
//
// This is the emulator's half of what unnamedCloudServices does for a
// dependency list, and it is the better half: SERVICES=s3,sqs,textract is a
// developer stating that this application calls Textract, where a dependency
// on boto3 states only that it calls AWS.
func unclaimedTokens(cloud string, tokens []string) []string {
	var out []string
	for _, tok := range tokens {
		claimed := false
		for _, tp := range thirdParties {
			if tp.catalogClaimsToken(cloud, tok) {
				claimed = true
				break
			}
		}
		if !claimed {
			out = append(out, tok)
		}
	}
	return out
}

// Note is the sentence af init prints about an emulator it found.
//
// It says what was found, what it produced, and what it did not produce. The
// third clause is the one worth the function: an emulator naming a service the
// catalog has no entry for is a refusal already scheduled, and the only moment
// it can be cheaply fixed is now, while somebody is reading this.
func (d DetectedEmulator) Note() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s runs %s (%s), which answers for %s.",
		d.Evidence, d.Service, d.Image, cloudDisplayName(d.Cloud))
	switch {
	case len(d.Services) == 0:
		fmt.Fprintf(&b, " It names no services, so no rules were written from it. "+
			"An emulator answering for anything asked of it cannot say which services this "+
			"application uses, and the rules came from the dependency list instead.")
	case len(d.Hosts) > 0:
		fmt.Fprintf(&b, " It names %s, so %s reached the network policy.",
			strings.Join(d.Services, ", "), plural(len(d.Hosts), "rule", "rules"))
	}
	if len(d.Unnamed) > 0 {
		them := "those services are"
		if len(d.Unnamed) == 1 {
			them = "that service is"
		}
		fmt.Fprintf(&b, " The catalog has no host for %s, so calls to %s refused with "+
			"no rule matches rather than by name. Write the rule into egress by hand, or add "+
			"an entry for it.", strings.Join(d.Unnamed, ", "), them)
	}
	return b.String()
}

// plural picks the word for a count, so a sentence about one rule does not say
// 1 rules. It is here rather than shared because these are the only two
// sentences in this package that count anything.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
