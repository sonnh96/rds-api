package resource

import (
	"errors"

	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
)

// driverError adapts a driver error to the shape apierr.From expects, so that
// the response carries the database's own error code and message.
type driverError struct {
	code    int
	message string
	err     error
}

func (e *driverError) Error() string { return e.message }

func (e *driverError) Unwrap() error { return e.err }

// DataAPIErrorCode reports the vendor error code and message.
func (e *driverError) DataAPIErrorCode() (int, string) { return e.code, e.message }

// wrapDriverError annotates a database error with its vendor code. Errors that
// are not driver errors, and errors that already carry a code, pass through.
func wrapDriverError(err error) error {
	if err == nil {
		return nil
	}

	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return &driverError{code: int(mysqlErr.Number), message: mysqlErr.Message, err: err}
	}

	// PostgreSQL has no numeric vendor code, and the JDBC driver reports 0 for
	// it too, so the SQLSTATE goes into the message instead.
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		message := pqErr.Message
		if pqErr.Detail != "" {
			message += ": " + pqErr.Detail
		}
		return &driverError{code: 0, message: message, err: err}
	}

	return err
}
