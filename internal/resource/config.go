// Package resource owns the database side of the Data API: which cluster ARN
// maps to which server, how connections are made, and how transactions are kept
// alive between requests.
//
// It is a port of Resource.kt, ResourceManager.kt and ConnectionManager.kt. The
// JDBC URL those files build is replaced by a driver-specific DSN, and the
// per-request java.sql.Connection is replaced by a pooled *sql.DB, which lets
// the proxy serve concurrent requests without reconnecting every time.
package resource

import (
	"fmt"
	"maps"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/sonnh96/local-data-api/internal/sqlparam"
)

// Engine identifies a supported database.
type Engine string

const (
	// MySQL selects github.com/go-sql-driver/mysql.
	MySQL Engine = "mysql"
	// Postgres selects github.com/lib/pq.
	Postgres Engine = "postgres"
)

// EngineFromName resolves the ENGINE environment variable. Any name containing
// "mysql" selects MySQL and anything else selects PostgreSQL, matching setup()
// in LocalDataApi.kt.
func EngineFromName(name string) Engine {
	if strings.Contains(strings.ToLower(name), "mysql") {
		return MySQL
	}
	return Postgres
}

// DriverName is the database/sql driver this engine registers under.
func (e Engine) DriverName() string {
	if e == MySQL {
		return "mysql"
	}
	return "postgres"
}

// ParameterStyle is the placeholder syntax this engine's driver expects.
func (e Engine) ParameterStyle() sqlparam.Style {
	if e == MySQL {
		return sqlparam.Question
	}
	return sqlparam.Dollar
}

// Config describes one resource ARN: the server behind it and any extra
// connection options.
type Config struct {
	Engine      Engine
	ResourceArn string
	Host        string
	Port        int
	// Options are extra DSN parameters, e.g. sslmode for PostgreSQL.
	Options map[string]string
}

// DSN builds the connection string for the given credentials and database.
func (c Config) DSN(userName, password, database string) (string, error) {
	switch c.Engine {
	case MySQL:
		return c.mysqlDSN(userName, password, database), nil
	case Postgres:
		return c.postgresDSN(userName, password, database), nil
	default:
		return "", fmt.Errorf("unsupported engine %q", c.Engine)
	}
}

func (c Config) mysqlDSN(userName, password, database string) string {
	cfg := mysql.NewConfig()
	cfg.User = userName
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	cfg.DBName = database
	cfg.AllowNativePasswords = true
	// Report matched rows rather than changed rows, so an UPDATE that writes the
	// same value it found still counts towards numberOfRecordsUpdated. That is
	// what the real service reports.
	cfg.ClientFoundRows = true
	// Keep DATETIME, DATE and TIME as text. The Data API renders them from their
	// string form, and decoding to time.Time first would lose the precision the
	// server actually stored.
	cfg.ParseTime = false

	if len(c.Options) > 0 {
		cfg.Params = maps.Clone(c.Options)
	}
	return cfg.FormatDSN()
}

func (c Config) postgresDSN(userName, password, database string) string {
	values := map[string]string{
		"host":     c.Host,
		"port":     strconv.Itoa(c.Port),
		"user":     userName,
		"password": password,
		"sslmode":  "disable",
	}
	if database != "" {
		values["dbname"] = database
	}
	maps.Copy(values, c.Options)

	pairs := make([]string, 0, len(values))
	for _, key := range slices.Sorted(maps.Keys(values)) {
		pairs = append(pairs, key+"="+quotePostgresValue(values[key]))
	}
	return strings.Join(pairs, " ")
}

// quotePostgresValue quotes a keyword/value DSN element for lib/pq.
func quotePostgresValue(value string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(value)
	return "'" + escaped + "'"
}
