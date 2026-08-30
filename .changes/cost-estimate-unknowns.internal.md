# fixed

`tools/cost estimate` had no price for `GP_Standard_D2ds_v4`, which is the SKU
zone redundant high availability forces and the largest single line in a
production bill. It also had none for `random_bytes` or
`azurerm_postgresql_flexible_server_configuration`, both of which the applied
staging stack has created since the beginning, so the estimator was already
exiting non-zero there.

High availability multiplied compute and not storage. Azure bills the standby
as a whole second server, disk included.
