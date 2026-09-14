// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package aurora

// The fault codes this provider decides on, against the codes RDS sends.
//
// RDS's service model gives each error shape a wire code, and for the instance
// and quota shapes it is not the shape's name. The codes below are copied from
// botocore's data/rds/2014-10-31/service-2.json, sha256
// 27fc5b72e4bd43c4efe708c8d48fc3e8a8f539373abf35fb649986346f5699bf, reading
// shapes.<name>.error.code, and falling back to the shape name where the model
// gives no code, which is what every AWS SDK does. The model is not vendored
// here, so this table is the transcription and the digest is how to check it.
//
// This is a structural test and it is paired with a behavioural one:
// TestDestroyingABranchWhoseWriterIsAlreadyDeletingSucceeds drives the one code
// AWS answered live through a fake that sends the model's spelling.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEveryFaultCodeIsTheCodeRDSSends(t *testing.T) {
	for constant, wire := range map[string]string{
		faultClusterNotFound:    "DBClusterNotFoundFault",
		faultInstanceNotFound:   "DBInstanceNotFound",
		faultClusterExists:      "DBClusterAlreadyExistsFault",
		faultInstanceExists:     "DBInstanceAlreadyExists",
		faultInvalidState:       "InvalidDBClusterStateFault",
		faultInvalidInstance:    "InvalidDBInstanceState",
		faultQuotaExceeded:      "DBClusterQuotaExceededFault",
		faultSnapshotQuota:      "SnapshotQuotaExceeded",
		faultStorageQuota:       "StorageQuotaExceeded",
		faultInvalidRestoreTime: "InvalidRestoreFault",
	} {
		require.Equal(t, wire, constant, "RDS sends %s, and a provider matching anything else never sees the fault", wire)
	}
}
