// Package sqlitex opens modernc.org/sqlite databases through a connector that
// keeps a caller's cancelled context away from the driver's query path.
package sqlitex

import (
	"context"
	"database/sql"
	"database/sql/driver"

	_ "modernc.org/sqlite"
)

// sqliteConn is the set of driver interfaces a modernc.org/sqlite connection
// implements and database/sql uses.
type sqliteConn interface {
	driver.Conn
	driver.ConnBeginTx
	driver.ConnPrepareContext
	driver.ExecerContext
	driver.QueryerContext
	driver.Pinger
	driver.SessionResetter
	driver.Validator
}

// Open opens the SQLite database at dsn through cancelSafeConnector. Use it
// instead of sql.Open("sqlite", dsn).
func Open(dsn string) (*sql.DB, error) {
	plain, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	base := plain.Driver()
	_ = plain.Close()
	return sql.OpenDB(cancelSafeConnector{dsn: dsn, driver: base}), nil
}

// cancelSafeConnector opens connections that never hand a cancellable context
// to the driver's query path.
//
// modernc.org/sqlite before v1.61.1 leaks the statement when the context ends
// while QueryContext runs: it has already stepped the first row, then drops the
// rows without closing them and returns ctx.Err(). database/sql returns the
// connection to the pool with that statement still open, so the connection
// keeps an old WAL read snapshot. The next BEGIN IMMEDIATE on it fails at once
// with SQLITE_BUSY_SNAPSHOT ("database is locked (517)"), and so does every
// later write that lands on it. Web stream pollers cancel their reads whenever
// a client drops the request, which is enough to poison a connection.
//
// v1.61.1 closes those rows, but cancelled reads still left connections in
// use for a while after every caller had returned, so they outlived db.Close
// and its files changed after Close (store's TestCancelledReadsDoNotBreakWrites
// failed its TempDir cleanup in 4 of 320 runs without this connector, and in
// none of 290 with it).
type cancelSafeConnector struct {
	dsn    string
	driver driver.Driver
}

func (c cancelSafeConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.driver.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	if full, ok := conn.(sqliteConn); ok {
		return cancelSafeConn{full}, nil
	}
	return conn, nil
}

func (c cancelSafeConnector) Driver() driver.Driver { return c.driver }

type cancelSafeConn struct{ sqliteConn }

// QueryContext runs the query without the caller's cancellation, so the driver
// never abandons an open statement, then honours a context that ended
// meanwhile by closing the rows itself. database/sql still closes the returned
// rows when the context ends during iteration, which finalizes the statement
// safely between steps.
func (c cancelSafeConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := c.sqliteConn.QueryContext(context.WithoutCancel(ctx), query, args)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	return rows, nil
}
