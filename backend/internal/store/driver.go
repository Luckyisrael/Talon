package store

// The store speaks to SQLite through database/sql via sql.Open("sqlite", …),
// which requires the modernc.org/sqlite driver to be registered. Registering it
// here — in the package that actually opens the connection — means every
// consumer of store.New (the server binary, tests, any future command) gets a
// working driver without having to know which one to import. Previously this
// blank import lived only in cmd/server/main.go, so store.New failed with
// "unknown driver \"sqlite\"" in any test binary that didn't reach main.
import _ "modernc.org/sqlite"
