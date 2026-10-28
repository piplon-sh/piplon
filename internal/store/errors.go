package store

import "errors"

// ErrSchemaTooNew means the database was migrated by a newer piplon.
var ErrSchemaTooNew = errors.New("database schema is newer than this piplon")

// ErrNoOpenIncident means an update or close named no open incident.
var ErrNoOpenIncident = errors.New("no such open incident")
