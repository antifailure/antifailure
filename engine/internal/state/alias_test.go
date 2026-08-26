package state_test

import "database/sql"

// sqlTx keeps the transaction callbacks readable without importing
// database/sql into every test signature.
type sqlTx = sql.Tx
