// Package models holds the request and response payloads of the RDS Data API.
//
// It is a port of Models.kt. Field names are the wire names used by the AWS
// SDKs, and every optional member is omitted from the JSON when unset so that
// responses match what the real service emits.
package models

import (
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sonnh96/local-data-api/internal/apierr"
)

// ArrayValue is the Data API representation of an array column.
type ArrayValue struct {
	ArrayValues   []ArrayValue `json:"arrayValues,omitempty"`
	BooleanValues []bool       `json:"booleanValues,omitempty"`
	DoubleValues  []float64    `json:"doubleValues,omitempty"`
	LongValues    []int64      `json:"longValues,omitempty"`
	StringValues  []string     `json:"stringValues,omitempty"`
}

// ArrayValueFromSlice builds an ArrayValue from a decoded array column. The
// element type of the first non-nil entry decides which typed slice is filled;
// a slice of slices becomes a nested ArrayValue.
func ArrayValueFromSlice(values []any) ArrayValue {
	var first any
	for _, v := range values {
		if v != nil {
			first = v
			break
		}
	}

	switch first.(type) {
	case string:
		out := make([]string, 0, len(values))
		for _, v := range values {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return ArrayValue{StringValues: out}
	case bool:
		out := make([]bool, 0, len(values))
		for _, v := range values {
			if b, ok := v.(bool); ok {
				out = append(out, b)
			}
		}
		return ArrayValue{BooleanValues: out}
	case float64:
		out := make([]float64, 0, len(values))
		for _, v := range values {
			if f, ok := v.(float64); ok {
				out = append(out, f)
			}
		}
		return ArrayValue{DoubleValues: out}
	case int64:
		out := make([]int64, 0, len(values))
		for _, v := range values {
			if i, ok := v.(int64); ok {
				out = append(out, i)
			}
		}
		return ArrayValue{LongValues: out}
	default:
		out := make([]ArrayValue, 0, len(values))
		for _, v := range values {
			if nested, ok := v.([]any); ok {
				out = append(out, ArrayValueFromSlice(nested))
			}
		}
		return ArrayValue{ArrayValues: out}
	}
}

// Blob is a byte value that is base64 encoded on the wire.
type Blob struct {
	EncodedValue string
}

// BlobFromBytes base64 encodes value.
func BlobFromBytes(value []byte) Blob {
	return Blob{EncodedValue: base64.StdEncoding.EncodeToString(value)}
}

// Field is a single value in a record or a bind parameter.
type Field struct {
	// BlobValue holds base64-encoded binary data.
	BlobValue    *string     `json:"blobValue,omitempty"`
	BooleanValue *bool       `json:"booleanValue,omitempty"`
	DoubleValue  *float64    `json:"doubleValue,omitempty"`
	IsNull       *bool       `json:"isNull,omitempty"`
	LongValue    *int64      `json:"longValue,omitempty"`
	StringValue  *string     `json:"stringValue,omitempty"`
	ArrayValue   *ArrayValue `json:"arrayValue,omitempty"`
}

// NullField returns a Field carrying isNull: true.
func NullField() Field {
	isNull := true
	return Field{IsNull: &isNull}
}

// StringField returns a Field carrying a stringValue.
func StringField(v string) Field { return Field{StringValue: &v} }

// LongField returns a Field carrying a longValue.
func LongField(v int64) Field { return Field{LongValue: &v} }

// DoubleField returns a Field carrying a doubleValue.
func DoubleField(v float64) Field { return Field{DoubleValue: &v} }

// BooleanField returns a Field carrying a booleanValue.
func BooleanField(v bool) Field { return Field{BooleanValue: &v} }

// BlobField returns a Field carrying a base64-encoded blobValue.
func BlobField(v Blob) Field { return Field{BlobValue: &v.EncodedValue} }

// FieldFromValue converts a decoded column value into a Field. A value of an
// unhandled type becomes isNull rather than an error, because a column the
// proxy cannot render is still a column the client asked for.
func FieldFromValue(value any) Field {
	switch v := value.(type) {
	case nil:
		return NullField()
	case Blob:
		return BlobField(v)
	case bool:
		return BooleanField(v)
	case float64:
		return DoubleField(v)
	case int64:
		return LongField(v)
	case string:
		return StringField(v)
	case []any:
		arr := ArrayValueFromSlice(v)
		return Field{ArrayValue: &arr}
	default:
		return NullField()
	}
}

// SqlParameter is a named bind parameter supplied by the client.
type SqlParameter struct {
	Name     string `json:"name"`
	Value    Field  `json:"value"`
	TypeHint string `json:"typeHint,omitempty"`
}

var (
	dateHintPattern      = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	timeHintPattern      = regexp.MustCompile(`^\d{2}:\d{2}:\d{2}(\.\d{1,9})?$`)
	timestampHintPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}(\.\d{1,9})?$`)
	uuidHintPattern      = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// CastValue converts the parameter into the value handed to the driver.
//
// String values are validated against their typeHint and then passed through as
// strings: both drivers leave the parameter type for the server to infer, so a
// string still binds to a uuid, json or date column.
func (p SqlParameter) CastValue() (any, error) {
	v := p.Value
	switch {
	case v.BlobValue != nil:
		decoded, err := base64.StdEncoding.DecodeString(*v.BlobValue)
		if err != nil {
			return nil, apierr.BadRequest("Invalid base64 value for parameter %s", p.Name)
		}
		return decoded, nil
	case v.BooleanValue != nil:
		return *v.BooleanValue, nil
	case v.DoubleValue != nil:
		return *v.DoubleValue, nil
	case v.LongValue != nil:
		return *v.LongValue, nil
	case v.StringValue != nil:
		return p.castStringValue(*v.StringValue)
	case v.ArrayValue != nil:
		return nil, apierr.BadRequest("Array parameters are not supported.")
	default:
		return nil, nil
	}
}

func (p SqlParameter) castStringValue(value string) (any, error) {
	invalid := func(hint string) error {
		return apierr.BadRequest("Invalid %s value for parameter %s: %s", hint, p.Name, value)
	}

	switch strings.ToUpper(p.TypeHint) {
	case "":
		return value, nil
	case "DATE":
		if !dateHintPattern.MatchString(value) {
			return nil, invalid("DATE")
		}
		if _, err := time.Parse("2006-01-02", value); err != nil {
			return nil, invalid("DATE")
		}
	case "TIME":
		if !timeHintPattern.MatchString(value) {
			return nil, invalid("TIME")
		}
	case "TIMESTAMP":
		if !timestampHintPattern.MatchString(value) {
			return nil, invalid("TIMESTAMP")
		}
	case "DECIMAL":
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return nil, invalid("DECIMAL")
		}
	case "UUID":
		if !uuidHintPattern.MatchString(value) {
			return nil, invalid("UUID")
		}
	case "JSON":
		// Passed through untouched; the server parses and validates it.
	default:
		return nil, apierr.BadRequest("Unsupported typeHint %s for parameter %s", p.TypeHint, p.Name)
	}
	return value, nil
}

// ColumnMetadata describes one column of a result set.
type ColumnMetadata struct {
	ArrayBaseColumnType int    `json:"arrayBaseColumnType"`
	IsAutoIncrement     bool   `json:"isAutoIncrement"`
	IsCaseSensitive     bool   `json:"isCaseSensitive"`
	IsCurrency          bool   `json:"isCurrency"`
	IsSigned            bool   `json:"isSigned"`
	Label               string `json:"label"`
	Name                string `json:"name"`
	Nullable            int    `json:"nullable"`
	Precision           int    `json:"precision"`
	Scale               int    `json:"scale"`
	SchemaName          string `json:"schemaName"`
	TableName           string `json:"tableName"`
	Type                int    `json:"type"`
	TypeName            string `json:"typeName"`
}

// ExecuteStatementRequest is the body of POST /Execute.
type ExecuteStatementRequest struct {
	ResourceArn           string         `json:"resourceArn"`
	SecretArn             string         `json:"secretArn"`
	Sql                   string         `json:"sql"`
	Database              string         `json:"database,omitempty"`
	ContinueAfterTimeout  *bool          `json:"continueAfterTimeout,omitempty"`
	IncludeResultMetadata bool           `json:"includeResultMetadata,omitempty"`
	Parameters            []SqlParameter `json:"parameters,omitempty"`
	Schema                string         `json:"schema,omitempty"`
	TransactionID         string         `json:"transactionId,omitempty"`
}

// ExecuteStatementResponse is the body of a successful POST /Execute.
//
// generatedFields and records are pointers so that an empty result is still
// rendered as `[]` while an inapplicable one is left out of the response
// entirely, which is how the real service behaves: a query carries records, an
// update carries generatedFields, and neither carries the other.
type ExecuteStatementResponse struct {
	NumberOfRecordsUpdated int64            `json:"numberOfRecordsUpdated"`
	GeneratedFields        *[]Field         `json:"generatedFields,omitempty"`
	Records                *[][]Field       `json:"records,omitempty"`
	ColumnMetadata         []ColumnMetadata `json:"columnMetadata,omitempty"`
}

// WithGeneratedFields returns the response with generatedFields set, so that an
// empty slice still reaches the client as `[]`.
func (r ExecuteStatementResponse) WithGeneratedFields(fields []Field) ExecuteStatementResponse {
	if fields == nil {
		fields = []Field{}
	}
	r.GeneratedFields = &fields
	return r
}

// WithRecords returns the response with records set, so that a query returning
// no rows still reaches the client as `[]`.
func (r ExecuteStatementResponse) WithRecords(records [][]Field) ExecuteStatementResponse {
	if records == nil {
		records = [][]Field{}
	}
	r.Records = &records
	return r
}

// BeginTransactionRequest is the body of POST /BeginTransaction.
type BeginTransactionRequest struct {
	ResourceArn string `json:"resourceArn"`
	SecretArn   string `json:"secretArn"`
	Schema      string `json:"schema,omitempty"`
	Database    string `json:"database,omitempty"`
}

// BeginTransactionResponse is the body of a successful POST /BeginTransaction.
type BeginTransactionResponse struct {
	TransactionID string `json:"transactionId"`
}

// CommitTransactionRequest is the body of POST /CommitTransaction.
type CommitTransactionRequest struct {
	ResourceArn   string `json:"resourceArn"`
	SecretArn     string `json:"secretArn"`
	TransactionID string `json:"transactionId"`
}

// CommitTransactionResponse is the body of a successful POST /CommitTransaction.
type CommitTransactionResponse struct {
	TransactionStatus string `json:"transactionStatus"`
}

// RollbackTransactionRequest is the body of POST /RollbackTransaction.
type RollbackTransactionRequest struct {
	ResourceArn   string `json:"resourceArn"`
	SecretArn     string `json:"secretArn"`
	TransactionID string `json:"transactionId"`
}

// RollbackTransactionResponse is the body of a successful POST /RollbackTransaction.
type RollbackTransactionResponse struct {
	TransactionStatus string `json:"transactionStatus"`
}

// BatchExecuteStatementRequest is the body of POST /BatchExecute.
type BatchExecuteStatementRequest struct {
	ResourceArn           string           `json:"resourceArn"`
	SecretArn             string           `json:"secretArn"`
	Sql                   string           `json:"sql"`
	Database              string           `json:"database,omitempty"`
	ContinueAfterTimeout  *bool            `json:"continueAfterTimeout,omitempty"`
	IncludeResultMetadata *bool            `json:"includeResultMetadata,omitempty"`
	ParameterSets         [][]SqlParameter `json:"parameterSets,omitempty"`
	Schema                string           `json:"schema,omitempty"`
	TransactionID         string           `json:"transactionId,omitempty"`
}

// UpdateResult holds the fields generated by one statement of a batch.
type UpdateResult struct {
	GeneratedFields []Field `json:"generatedFields"`
}

// BatchExecuteStatementResponse is the body of a successful POST /BatchExecute.
type BatchExecuteStatementResponse struct {
	UpdateResults []UpdateResult `json:"updateResults"`
}

// ErrorResponse is the body served for any failed request.
type ErrorResponse struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

// ExecuteSQLRequest is the body of POST /ExecuteSql, which is not implemented.
type ExecuteSQLRequest struct {
	AwsSecretStoreArn      string `json:"awsSecretStoreArn"`
	DbClusterOrInstanceArn string `json:"dbClusterOrInstanceArn"`
	SqlStatements          string `json:"sqlStatements"`
	Database               string `json:"database,omitempty"`
	Schema                 string `json:"schema,omitempty"`
}
