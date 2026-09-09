// Package testdb is an in-process database/sql driver that serves canned
// results. It lets the resource and server packages be tested end to end
// without a MySQL or PostgreSQL server.
package testdb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"sync"
)

// Column describes one column of a canned result set.
type Column struct {
	Name     string
	TypeName string
	// Nullable is reported only when HasNullable is set, mirroring drivers that
	// cannot tell.
	Nullable    bool
	HasNullable bool
}

// ResultSet is a canned response to a query.
type ResultSet struct {
	Columns []Column
	Rows    [][]driver.Value
}

// ExecResult is a canned response to an update.
type ExecResult struct {
	LastInsertID int64
	RowsAffected int64
	// LastInsertIDErr makes LastInsertId fail, as the PostgreSQL driver does.
	LastInsertIDErr error
}

// Call records one statement the code under test ran.
type Call struct {
	Query string
	Args  []driver.Value
	Kind  string // "query" or "exec"
}

// DB is a scripted database. Set OnQuery and OnExec to decide what statements
// return; both default to an empty result.
type DB struct {
	mu     sync.Mutex
	calls  []Call
	events []string

	OnQuery func(query string, args []driver.Value) (*ResultSet, error)
	OnExec  func(query string, args []driver.Value) (*ExecResult, error)
}

// New returns an empty scripted database.
func New() *DB { return &DB{} }

// Open returns a *sql.DB backed by this scripted database.
func (d *DB) Open() *sql.DB { return sql.OpenDB(connector{db: d}) }

// Opener returns a function suitable for resource.Manager.SetOpener.
func (d *DB) Opener() func(driverName, dsn string) (*sql.DB, error) {
	return func(string, string) (*sql.DB, error) { return d.Open(), nil }
}

// Calls returns the statements run so far.
func (d *DB) Calls() []Call {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Call(nil), d.calls...)
}

// Events returns the transaction lifecycle events seen so far, in order: one of
// "begin", "commit", "rollback" or "close" per entry.
func (d *DB) Events() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.events...)
}

// Count returns how many times an event was recorded.
func (d *DB) Count(event string) int {
	total := 0
	for _, seen := range d.Events() {
		if seen == event {
			total++
		}
	}
	return total
}

func (d *DB) record(call Call) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, call)
}

func (d *DB) event(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, name)
}

func (d *DB) query(q string, args []driver.Value) (driver.Rows, error) {
	d.record(Call{Query: q, Args: args, Kind: "query"})
	if d.OnQuery == nil {
		return &rows{}, nil
	}
	set, err := d.OnQuery(q, args)
	if err != nil {
		return nil, err
	}
	if set == nil {
		set = &ResultSet{}
	}
	return &rows{set: set}, nil
}

func (d *DB) exec(q string, args []driver.Value) (driver.Result, error) {
	d.record(Call{Query: q, Args: args, Kind: "exec"})
	if d.OnExec == nil {
		return result{}, nil
	}
	out, err := d.OnExec(q, args)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = &ExecResult{}
	}
	return result{ExecResult: *out}, nil
}

// connector opens connections onto a scripted DB.
type connector struct{ db *DB }

func (c connector) Connect(context.Context) (driver.Conn, error) { return &conn{db: c.db}, nil }

func (c connector) Driver() driver.Driver { return fakeDriver{db: c.db} }

type fakeDriver struct{ db *DB }

func (d fakeDriver) Open(string) (driver.Conn, error) { return &conn{db: d.db}, nil }

type conn struct{ db *DB }

var (
	_ driver.Conn           = (*conn)(nil)
	_ driver.ConnBeginTx    = (*conn)(nil)
	_ driver.ExecerContext  = (*conn)(nil)
	_ driver.QueryerContext = (*conn)(nil)
)

func (c *conn) Prepare(query string) (driver.Stmt, error) {
	return &stmt{db: c.db, query: query}, nil
}

func (c *conn) Close() error {
	c.db.event("close")
	return nil
}

func (c *conn) Begin() (driver.Tx, error) { return c.BeginTx(context.Background(), driver.TxOptions{}) }

func (c *conn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	c.db.event("begin")
	return tx{db: c.db}, nil
}

func (c *conn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.db.exec(query, values(args))
}

func (c *conn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return c.db.query(query, values(args))
}

type tx struct{ db *DB }

func (t tx) Commit() error {
	t.db.event("commit")
	return nil
}

func (t tx) Rollback() error {
	t.db.event("rollback")
	return nil
}

type stmt struct {
	db    *DB
	query string
}

var (
	_ driver.Stmt             = (*stmt)(nil)
	_ driver.StmtExecContext  = (*stmt)(nil)
	_ driver.StmtQueryContext = (*stmt)(nil)
)

// NumInput reports -1 so database/sql does not check the argument count.
func (s *stmt) NumInput() int { return -1 }

func (s *stmt) Close() error { return nil }

func (s *stmt) Exec(args []driver.Value) (driver.Result, error) { return s.db.exec(s.query, args) }

func (s *stmt) Query(args []driver.Value) (driver.Rows, error) { return s.db.query(s.query, args) }

func (s *stmt) ExecContext(_ context.Context, args []driver.NamedValue) (driver.Result, error) {
	return s.db.exec(s.query, values(args))
}

func (s *stmt) QueryContext(_ context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return s.db.query(s.query, values(args))
}

type result struct{ ExecResult }

func (r result) LastInsertId() (int64, error) {
	if r.LastInsertIDErr != nil {
		return 0, r.LastInsertIDErr
	}
	return r.LastInsertID, nil
}

func (r result) RowsAffected() (int64, error) { return r.ExecResult.RowsAffected, nil }

type rows struct {
	set *ResultSet
	pos int
}

var (
	_ driver.Rows                           = (*rows)(nil)
	_ driver.RowsColumnTypeDatabaseTypeName = (*rows)(nil)
	_ driver.RowsColumnTypeNullable         = (*rows)(nil)
)

func (r *rows) Columns() []string {
	if r.set == nil {
		return nil
	}
	names := make([]string, len(r.set.Columns))
	for i, column := range r.set.Columns {
		names[i] = column.Name
	}
	return names
}

func (r *rows) Close() error { return nil }

func (r *rows) Next(dest []driver.Value) error {
	if r.set == nil || r.pos >= len(r.set.Rows) {
		return io.EOF
	}
	copy(dest, r.set.Rows[r.pos])
	r.pos++
	return nil
}

func (r *rows) ColumnTypeDatabaseTypeName(index int) string {
	return r.set.Columns[index].TypeName
}

func (r *rows) ColumnTypeNullable(index int) (nullable, ok bool) {
	column := r.set.Columns[index]
	return column.Nullable, column.HasNullable
}

func values(named []driver.NamedValue) []driver.Value {
	out := make([]driver.Value, len(named))
	for i, arg := range named {
		out[i] = arg.Value
	}
	return out
}
