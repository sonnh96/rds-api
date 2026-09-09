package resource

import (
	"context"
	"database/sql"

	"github.com/sonnh96/local-data-api/internal/apierr"
	"github.com/sonnh96/local-data-api/internal/dataapi"
	"github.com/sonnh96/local-data-api/internal/jdbc"
	"github.com/sonnh96/local-data-api/internal/models"
	"github.com/sonnh96/local-data-api/internal/sqlparam"
)

// Resource is one request's handle on a database transaction.
type Resource struct {
	// TransactionID is set when the request joined an existing transaction.
	TransactionID string

	engine   Engine
	manager  *Manager
	tx       *sql.Tx
	conn     *sql.Conn
	database string
}

// Engine reports which database this resource talks to.
func (r *Resource) Engine() Engine { return r.engine }

// InTransaction reports whether the caller owns the transaction, in which case
// the handler must not commit or close it.
func (r *Resource) InTransaction() bool { return r.TransactionID != "" }

// Begin parks the transaction under a fresh ID so later requests can join it.
func (r *Resource) Begin() string {
	transactionID := NewTransactionID()
	r.TransactionID = transactionID
	r.manager.putTransaction(transactionID, &transaction{
		tx:       r.tx,
		conn:     r.conn,
		database: r.database,
	})
	return transactionID
}

// Commit commits the transaction.
func (r *Resource) Commit() error {
	return wrapDriverError(r.tx.Commit())
}

// Rollback rolls the transaction back.
func (r *Resource) Rollback() error {
	return wrapDriverError(r.tx.Rollback())
}

// Close returns the connection to the pool and forgets the transaction.
func (r *Resource) Close() error {
	if r.TransactionID != "" {
		r.manager.deleteTransaction(r.TransactionID)
	}
	return r.conn.Close()
}

// Execute runs one statement and renders the result as a Data API response.
func (r *Resource) Execute(ctx context.Context, sqlText string, parameters []models.SqlParameter, includeResultMetadata bool) (models.ExecuteStatementResponse, error) {
	statement, args, err := r.prepare(sqlText, parameters)
	if err != nil {
		return models.ExecuteStatementResponse{}, err
	}

	if sqlparam.ReturnsRows(sqlText) {
		return r.query(ctx, statement, args, includeResultMetadata)
	}
	return r.exec(ctx, statement, args)
}

// BatchExecute runs one statement once per parameter set.
//
// database/sql has no batch API, so the statement is prepared once and executed
// per parameter set. That yields the generated key of each individual execution
// rather than of the batch as a whole.
func (r *Resource) BatchExecute(ctx context.Context, sqlText string, parameterSets [][]models.SqlParameter) (models.BatchExecuteStatementResponse, error) {
	response := models.BatchExecuteStatementResponse{UpdateResults: []models.UpdateResult{}}
	if len(parameterSets) == 0 {
		return response, nil
	}

	parsed := sqlparam.Parse(sqlText, r.engine.ParameterStyle())
	prepared, err := r.tx.PrepareContext(ctx, parsed.SQL)
	if err != nil {
		return response, wrapDriverError(err)
	}
	defer prepared.Close()

	for _, parameterSet := range parameterSets {
		params, err := parameterMap(parameterSet)
		if err != nil {
			return response, err
		}
		args, err := parsed.Args(params)
		if err != nil {
			return response, err
		}

		result, err := prepared.ExecContext(ctx, args...)
		if err != nil {
			return response, wrapDriverError(err)
		}
		response.UpdateResults = append(response.UpdateResults, models.UpdateResult{
			GeneratedFields: generatedFields(result),
		})
	}

	return response, nil
}

// prepare rewrites the named parameters and resolves the argument list. A
// statement sent without parameters is passed through untouched, so a stray `:`
// in it cannot be mistaken for a bind parameter.
func (r *Resource) prepare(sqlText string, parameters []models.SqlParameter) (string, []any, error) {
	if len(parameters) == 0 {
		return sqlText, nil, nil
	}

	params, err := parameterMap(parameters)
	if err != nil {
		return "", nil, err
	}

	parsed := sqlparam.Parse(sqlText, r.engine.ParameterStyle())
	args, err := parsed.Args(params)
	if err != nil {
		return "", nil, err
	}
	return parsed.SQL, args, nil
}

func parameterMap(parameters []models.SqlParameter) (map[string]any, error) {
	params := make(map[string]any, len(parameters))
	for _, parameter := range parameters {
		value, err := parameter.CastValue()
		if err != nil {
			return nil, err
		}
		params[parameter.Name] = value
	}
	return params, nil
}

func (r *Resource) query(ctx context.Context, statement string, args []any, includeResultMetadata bool) (models.ExecuteStatementResponse, error) {
	rows, err := r.tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return models.ExecuteStatementResponse{}, wrapDriverError(err)
	}
	defer rows.Close()

	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		return models.ExecuteStatementResponse{}, wrapDriverError(err)
	}

	columns := make([]dataapi.Column, len(columnTypes))
	for i, columnType := range columnTypes {
		columns[i] = dataapi.NewColumn(string(r.engine), columnType.DatabaseTypeName())
	}

	records := [][]models.Field{}
	for rows.Next() {
		scanned := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range scanned {
			targets[i] = &scanned[i]
		}
		if err := rows.Scan(targets...); err != nil {
			return models.ExecuteStatementResponse{}, wrapDriverError(err)
		}

		record := make([]models.Field, len(columns))
		for i, column := range columns {
			field, err := dataapi.FieldFor(column, scanned[i])
			if err != nil {
				return models.ExecuteStatementResponse{}, apierr.BadRequest("%s", err.Error())
			}
			record[i] = field
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return models.ExecuteStatementResponse{}, wrapDriverError(err)
	}

	response := models.ExecuteStatementResponse{NumberOfRecordsUpdated: 0}.WithRecords(records)
	if includeResultMetadata {
		response.ColumnMetadata = columnMetadata(r.engine, columnTypes)
	}
	return response, nil
}

func (r *Resource) exec(ctx context.Context, statement string, args []any) (models.ExecuteStatementResponse, error) {
	result, err := r.tx.ExecContext(ctx, statement, args...)
	if err != nil {
		return models.ExecuteStatementResponse{}, wrapDriverError(err)
	}

	updated, err := result.RowsAffected()
	if err != nil || updated < 0 {
		updated = 0
	}

	response := models.ExecuteStatementResponse{NumberOfRecordsUpdated: updated}
	return response.WithGeneratedFields(generatedFields(result)), nil
}

// generatedFields reports the auto-generated key of an insert. PostgreSQL has no
// such concept outside a RETURNING clause and its driver reports an error here,
// which correctly leaves the list empty.
func generatedFields(result sql.Result) []models.Field {
	id, err := result.LastInsertId()
	if err != nil || id <= 0 {
		return []models.Field{}
	}
	return []models.Field{models.LongField(id)}
}

// columnMetadata describes the result columns.
//
// Go's database/sql exposes far less than JDBC's ResultSetMetaData: the table
// and schema a column came from, and whether it auto-increments, are simply not
// available, so those members report their zero value.
func columnMetadata(engine Engine, columnTypes []*sql.ColumnType) []models.ColumnMetadata {
	metadata := make([]models.ColumnMetadata, len(columnTypes))
	for i, columnType := range columnTypes {
		typeName := columnType.DatabaseTypeName()
		jdbcType := jdbc.TypeFromName(string(engine), typeName)

		// JDBC reports 0 for "not nullable", 1 for "nullable" and 2 when it
		// cannot tell; the Go drivers report the same three states.
		nullable := 2
		if isNullable, ok := columnType.Nullable(); ok {
			nullable = 0
			if isNullable {
				nullable = 1
			}
		}

		precision := 0
		scale := 0
		if p, s, ok := columnType.DecimalSize(); ok {
			precision, scale = int(p), int(s)
		} else if length, ok := columnType.Length(); ok {
			precision = int(length)
		}

		metadata[i] = models.ColumnMetadata{
			ArrayBaseColumnType: 0,
			IsAutoIncrement:     false,
			IsCaseSensitive:     false,
			IsCurrency:          false,
			IsSigned:            jdbc.IsSigned(jdbcType),
			Label:               columnType.Name(),
			Name:                columnType.Name(),
			Nullable:            nullable,
			Precision:           precision,
			Scale:               scale,
			SchemaName:          "",
			TableName:           "",
			Type:                jdbcType,
			TypeName:            typeName,
		}
	}
	return metadata
}
