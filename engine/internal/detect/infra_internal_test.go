package detect

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Which module sources are directories in this repository, asserted where the
// decision is made.
//
// WHY THIS IS AN INTERNAL TEST AND NOT AN END TO END ONE. A module call
// excludes a directory from the draft. A call to hashicorp/consul/aws
// "excludes" infra/hashicorp/consul/aws, which is not a directory anybody has,
// so it excludes nothing and the drafted paths come out identical either way.
// That means the end to end answer CANNOT distinguish a scan that knows a
// registry address from one that does not, and a test written out there would
// pass whatever the rule did. The distinction becomes visible one layer down,
// so that is where it is held, and it matters the moment a registry address
// happens to collide with a real directory name.

func TestLocalModuleTargets_OnlyLocalPathsAreDirectories(t *testing.T) {
	t.Parallel()
	body := `
module "consul" {
  source  = "hashicorp/consul/aws"
  version = "0.11.0"
}

module "vpc" {
  source = "git::https://example.com/vpc.git?ref=v1"
}

module "registry_with_subdir" {
  source = "terraform-aws-modules/vpc/aws//modules/vpc-endpoints"
}

module "network" {
  source = "./network"
}

module "shared" {
  source = "../shared/logging"
}
`
	require.Equal(t,
		[]string{"infra/network", "shared/logging"},
		localModuleTargets(body, "infra"),
		"only a source beginning ./ or ../ names a directory in this repository")
}

func TestLocalModuleTargets_ASourceOutsideAModuleBlockIsNotAModuleCall(t *testing.T) {
	t.Parallel()
	// required_providers carries a source too, and it means something else.
	// Read as a module call it would silently remove a real root module from
	// the draft, and a stack quietly missing from a manifest is invisible:
	// the section would simply not mention it.
	body := `
terraform {
  required_providers {
    local = {
      source = "./not-a-module"
    }
  }
}

module "network" {
  source = "./network"
}
`
	require.Equal(t, []string{"infra/network"}, localModuleTargets(body, "infra"))
}

func TestLocalModuleTargets_AModuleBlockIsReadToItsOwnClosingBrace(t *testing.T) {
	t.Parallel()
	// A module block contains blocks of its own, so the scan has to count
	// braces rather than stop at the first one. If it stopped early, a source
	// written after a nested block would be missed and the callee would be
	// drafted as a root module of its own.
	body := `
module "network" {
  providers = {
    aws = aws.primary
  }

  source = "./network"
}

resource "null_resource" "after" {
  triggers = {
    source = "./not-a-module"
  }
}
`
	require.Equal(t, []string{"infra/network"}, localModuleTargets(body, "infra"))
}

func TestModuleBlocks_AnUnbalancedFileStillContributesItsCalls(t *testing.T) {
	t.Parallel()
	// A truncated file does not parse as Terraform either, so nothing here is
	// rescuing a working repository. What it avoids is the scan silently
	// dropping the calls it already read and drafting a child module as a
	// root, which would be a wrong answer rather than a missing one.
	body := `
module "network" {
  source = "./network"
`
	require.Equal(t, []string{"infra/network"}, localModuleTargets(body, "infra"))
}
