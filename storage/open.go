package storage

import "fmt"

// Open creates a StoreReader for the given driver and DSN.
// driver must be "sqlite" or "postgres".
// For SQLite, dsn is the file path (use ":memory:" for in-process testing).
// For PostgreSQL, dsn is a standard connection string, e.g.
// "postgres://user:pass@host:5432/dbname".
func Open(driver, dsn string) (StoreReader, error) {
	switch driver {
	case "sqlite":
		return openSQLite(dsn)
	case "postgres":
		return openPostgres(dsn)
	default:
		return nil, fmt.Errorf("storage: unsupported driver %q: must be \"sqlite\" or \"postgres\"", driver)
	}
}
