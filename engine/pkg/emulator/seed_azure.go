package emulator

import (
	"fmt"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// What an Azure declaration becomes inside Azurite, which is NOTHING YET, and
// this file exists so that a reader finds that out here rather than from a
// twin that is quietly missing a container.
//
// THE MEASUREMENT, taken on 2026-09-20 against the Azurite digest this build
// pins. A request to create a blob container, addressed the way an environment
// addresses one, was refused:
//
//	PUT /afprobe?restype=container
//	Host: devstoreaccount1.blob.core.windows.net
//	x-ms-version: 2021-08-06
//	x-ms-date: <now>
//
//	HTTP/1.1 403 Server failed to authenticate the request.
//	x-ms-error-code: AuthorizationFailure
//
// Azurite validates the Shared Key signature on every request. LocalStack and
// both Google emulators answer an unsigned one, which is why those three are
// seeded by this build and this one is not: signing needs the storage
// account's key, and nothing in an environment hands this process one. The
// account credential an application receives is a SUBSTITUTED credential that
// the manifest decides, and reading it here would mean this package knowing
// about manifests, which is the coupling pkg/emulator exists without.
//
// A SECOND THING THE MEASUREMENT FOUND, recorded because it is the trap the
// next person hits and it costs an hour. With production style addressing the
// account is in the HOSTNAME, and Azurite then refuses a path that ALSO names
// the account, with a bare 400 and an empty body. So the path is /<container>
// and not /devstoreaccount1/<container>. A 400 with no body reads as a
// malformed request rather than as a naming rule, and it is the answer to the
// request everybody writes first.
//
// The three types below therefore report an unmeasured outcome carrying that
// reason. Reporting is the whole point: a declaration silently absent from the
// plan is a container missing from the twin that nothing ever mentioned, and
// this product exists to refuse exactly that.

func init() {
	registerSeed("azurerm_storage_container", planAzureUnseeded("blob container", AzureBlobName))
	registerSeed("azurerm_storage_queue", planAzureUnseeded("queue", AzureQueueName))
	registerSeed("azurerm_storage_table", planAzureUnseeded("table", AzureTableName))
}

// azuriteUnsignedReason is why nothing is created in Azurite, in one sentence
// somebody can act on.
const azuriteUnsignedReason = "Azurite refuses an unsigned request with 403 AuthorizationFailure, " +
	"measured against the digest this build pins, and this build holds no account key to sign " +
	"one with, so the %s is not created in the twin and an application that expects it will not " +
	"find it"

// planAzureUnseeded is the plan for an Azure resource this build declares it
// does not create.
//
// A planner rather than no planner at all, and the difference matters. With no
// planner the declaration would fall to the unknown type arm, whose message
// says no emulator in this build answers for the type, and for Azure Blob
// Storage that sentence is FALSE: the emulator is here, it is started, and the
// application reaches it. What is missing is the seeding, and a reader sent
// looking for a missing emulator would be looking in the wrong place.
func planAzureUnseeded(kind, emulatorName string) planner {
	return func(d provider.CloudResource) Step {
		return Step{
			Emulator: emulatorName,
			Kind:     kind,
			Outcomes: []Outcome{{
				Name:   d.Name,
				State:  Unmeasured,
				Reason: fmt.Sprintf(azuriteUnsignedReason, kind),
			}},
		}
	}
}
