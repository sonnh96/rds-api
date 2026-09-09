package resource

import (
	"context"
	"crypto/rand"
	"database/sql"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sonnh96/local-data-api/internal/apierr"
)

const (
	transactionIDCharacters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ/=+"
	transactionIDLength     = 184
)

// NewTransactionID returns an opaque transaction identifier shaped like the one
// the real service issues.
func NewTransactionID() string {
	buf := make([]byte, transactionIDLength)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand does not fail on any supported platform; if it ever does,
		// there is nothing sensible to fall back to.
		panic("local-data-api: cannot read random bytes: " + err.Error())
	}
	for i, b := range buf {
		buf[i] = transactionIDCharacters[int(b)%len(transactionIDCharacters)]
	}
	return string(buf)
}

// transaction is an open transaction parked between requests.
type transaction struct {
	tx       *sql.Tx
	conn     *sql.Conn
	database string
}

// Manager owns the resource configuration, the per-database connection pools
// and the open transactions.
type Manager struct {
	mu           sync.RWMutex
	configs      map[string]Config
	pools        map[string]*sql.DB
	transactions map[string]*transaction

	// openDB is swapped out in tests to avoid needing a real server.
	openDB func(driverName, dsn string) (*sql.DB, error)
}

// SetOpener replaces how the manager opens a database handle. It is the seam
// tests use to run against an in-process driver instead of a real server.
func (m *Manager) SetOpener(open func(driverName, dsn string) (*sql.DB, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.openDB = open
}

// NewManager returns an empty Manager.
func NewManager() *Manager {
	return &Manager{
		configs:      make(map[string]Config),
		pools:        make(map[string]*sql.DB),
		transactions: make(map[string]*transaction),
		openDB:       sql.Open,
	}
}

// SetResource registers the server behind a resource ARN.
func (m *Manager) SetResource(engine Engine, resourceArn, host string, port int, options map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configs[resourceArn] = Config{
		Engine:      engine,
		ResourceArn: resourceArn,
		Host:        host,
		Port:        port,
		Options:     maps.Clone(options),
	}
}

// Config returns the configuration registered for a resource ARN.
func (m *Manager) Config(resourceArn string) (Config, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	config, ok := m.configs[resourceArn]
	return config, ok
}

// HasTransaction reports whether a transaction is open under this ID.
func (m *Manager) HasTransaction(transactionID string) bool {
	if transactionID == "" {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.transactions[transactionID]
	return ok
}

func (m *Manager) putTransaction(transactionID string, t *transaction) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.transactions[transactionID] = t
}

func (m *Manager) takeTransaction(transactionID string) (*transaction, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.transactions[transactionID]
	return t, ok
}

func (m *Manager) deleteTransaction(transactionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.transactions, transactionID)
}

// db returns the pooled *sql.DB for one resource, credential and database
// combination, opening it on first use.
func (m *Manager) db(config Config, userName, password, database string) (*sql.DB, error) {
	key := strings.Join([]string{config.ResourceArn, userName, password, database}, "\x00")

	m.mu.RLock()
	pooled, ok := m.pools[key]
	m.mu.RUnlock()
	if ok {
		return pooled, nil
	}

	dsn, err := config.DSN(userName, password, database)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if pooled, ok := m.pools[key]; ok {
		return pooled, nil
	}

	opened, err := m.openDB(config.Engine.DriverName(), dsn)
	if err != nil {
		return nil, wrapDriverError(err)
	}
	opened.SetMaxIdleConns(4)
	opened.SetConnMaxIdleTime(5 * time.Minute)
	m.pools[key] = opened

	return opened, nil
}

// GetResource resolves a request onto a Resource, either by opening a new
// transaction or by picking up the one the transaction ID names.
//
// The error for an unknown resource ARN depends on whether the transaction is
// known: a live transaction against an unregistered ARN is a server bug rather
// than a client mistake, which is the distinction ResourceManager.kt draws.
func (m *Manager) GetResource(ctx context.Context, resourceArn, userName, password, transactionID, database, schema string) (*Resource, error) {
	config, ok := m.Config(resourceArn)
	if !ok {
		if m.HasTransaction(transactionID) {
			return nil, apierr.InternalServerError()
		}
		return nil, apierr.BadRequest("HttpEndPoint is not enabled for %s", resourceArn)
	}

	if transactionID == "" {
		return m.openResource(ctx, config, userName, password, database, schema)
	}

	existing, ok := m.takeTransaction(transactionID)
	if !ok {
		return nil, apierr.BadRequest("Invalid transaction ID")
	}
	if database != "" && existing.database != "" && database != existing.database {
		return nil, apierr.BadRequest("Database name is not the same as when transaction was created")
	}

	return &Resource{
		engine:        config.Engine,
		manager:       m,
		tx:            existing.tx,
		conn:          existing.conn,
		database:      existing.database,
		TransactionID: transactionID,
	}, nil
}

// openResource takes a connection out of the pool and starts a transaction on
// it. Every statement runs inside a transaction: a statement sent without a
// transaction ID is committed by the handler as its own unit of work.
func (m *Manager) openResource(ctx context.Context, config Config, userName, password, database, schema string) (*Resource, error) {
	pooled, err := m.db(config, userName, password, database)
	if err != nil {
		return nil, err
	}

	conn, err := pooled.Conn(ctx)
	if err != nil {
		return nil, wrapDriverError(err)
	}

	if schema != "" {
		if err := applySchema(ctx, conn, config.Engine, schema); err != nil {
			conn.Close()
			return nil, err
		}
	}

	// The transaction must not be tied to the request context: a transaction
	// opened by BeginTransaction outlives the request that opened it, and
	// database/sql rolls a transaction back as soon as the context it was
	// begun with is cancelled. Individual statements still run under the
	// request context, so a disconnecting client still cancels its own query.
	tx, err := conn.BeginTx(context.WithoutCancel(ctx), nil)
	if err != nil {
		conn.Close()
		return nil, wrapDriverError(err)
	}

	return &Resource{
		engine:   config.Engine,
		manager:  m,
		tx:       tx,
		conn:     conn,
		database: database,
	}, nil
}

// applySchema selects the default schema for the connection. PostgreSQL calls
// it a search path; MySQL has no equivalent, so the database name already
// carried in the DSN is the schema there.
func applySchema(ctx context.Context, conn *sql.Conn, engine Engine, schema string) error {
	if engine != Postgres {
		return nil
	}
	if _, err := conn.ExecContext(ctx, "SET search_path TO "+quoteIdentifier(schema)); err != nil {
		return wrapDriverError(err)
	}
	return nil
}

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// Close releases every pooled connection. Open transactions are rolled back.
func (m *Manager) Close() error {
	m.mu.Lock()
	transactions := slices.Collect(maps.Values(m.transactions))
	pools := slices.Collect(maps.Values(m.pools))
	m.transactions = make(map[string]*transaction)
	m.pools = make(map[string]*sql.DB)
	m.mu.Unlock()

	var firstErr error
	for _, t := range transactions {
		t.tx.Rollback()
		if err := t.conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, pooled := range pools {
		if err := pooled.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
