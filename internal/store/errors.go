package store

import "errors"

// ErrSchemaTooNew means the database was migrated by a newer piplon.
var ErrSchemaTooNew = errors.New("database schema is newer than this piplon")
