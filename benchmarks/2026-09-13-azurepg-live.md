# azurepg, first golden and branch time on Azure

Run on 2026-09-13T04:49:36Z UTC, against commit `8a639dcc46f2`.

- Service: Azure Database for PostgreSQL Flexible Server, PostgreSQL 16.
- Server size: `Standard_B1ms`, Burstable, 32 GiB storage, no high availability, private access in a delegated subnet.
- Region: `centralus`.
- Client: a container in the same virtual network, running `TestLivePrivateAzureRestoreMaskBranchAndDelete` in `ee/engine/db/azurepg/live_test.go`.
- Source data: one synthetic row, so every number here is fixed cost rather than a rate.

| Step | Seconds | What it includes |
| --- | --- | --- |
| First golden | 420.3 | a point in time restore of the source, the mask, and the verification |
| Branch | 518.3 | waiting for the golden's first backup, a point in time restore of the golden, and preparation |

The branch includes the wait for the golden's first backup, because Azure answers a restore time earlier than a server's earliest restore point with InternalServerError, and a golden published minutes earlier has no backup yet. A branch from a golden that already has one skips that wait.

Neither number says how restore time grows with the size of the database. Microsoft gives the overall recovery as a few minutes up to a few hours, and a nearly empty server measures only the fixed part of that.
