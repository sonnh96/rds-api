package dataapi

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sonnh96/local-data-api/internal/jdbc"
	"github.com/sonnh96/local-data-api/internal/models"
)

// Column carries the little the Data API needs to know about a result column.
type Column struct {
	// Engine is "mysql" or "postgres"; it selects the type-name vocabulary.
	Engine string
	// DatabaseTypeName is the driver's name for the type, e.g. INT4 or DATETIME.
	DatabaseTypeName string
	// JDBCType is DatabaseTypeName resolved to a java.sql.Types constant.
	JDBCType int
}

// NewColumn resolves a driver type name into a Column.
func NewColumn(engine, databaseTypeName string) Column {
	return Column{
		Engine:           engine,
		DatabaseTypeName: databaseTypeName,
		JDBCType:         jdbc.TypeFromName(engine, databaseTypeName),
	}
}

// FieldFor renders a scanned column value as a Data API field, following the
// JDBC-type-to-Data-API-type table from the AWS documentation:
//
//	INTEGER, TINYINT, SMALLINT, BIGINT                     -> LONG
//	FLOAT, REAL, DOUBLE                                    -> DOUBLE
//	BOOLEAN, BIT                                           -> BOOLEAN
//	BLOB, BINARY, LONGVARBINARY, VARBINARY                 -> BLOB
//	everything else, dates and times included               -> STRING
func FieldFor(col Column, raw any) (models.Field, error) {
	value, err := ConvertValue(col, raw)
	if err != nil {
		return models.Field{}, err
	}
	return models.FieldFromValue(value), nil
}

// ConvertValue turns a scanned value into one of nil, int64, float64, bool,
// string, models.Blob or []any.
func ConvertValue(col Column, raw any) (any, error) {
	if raw == nil {
		return nil, nil
	}

	t := col.JDBCType
	typeName := strings.ToLower(strings.TrimSpace(col.DatabaseTypeName))

	switch {
	case jdbc.IsLong(t):
		return toInt64(raw)
	case jdbc.IsDouble(t):
		return toFloat64(raw)
	case jdbc.IsBoolean(t):
		return toBool(raw)
	case jdbc.IsBlob(t):
		bytes, err := toBytes(raw)
		if err != nil {
			return nil, err
		}
		return models.BlobFromBytes(bytes), nil
	case t == jdbc.TimestampWithTimezone || (t == jdbc.Timestamp && typeName == "timestamptz"):
		if instant, ok := raw.(time.Time); ok {
			return ConvertOffsetDatetimeValueToUTC(instant), nil
		}
		text, err := toString(raw)
		if err != nil {
			return nil, err
		}
		return ConvertOffsetDatetimeToUTC(text)
	case t == jdbc.Timestamp:
		if instant, ok := raw.(time.Time); ok {
			return FormatDatetimeValue(instant), nil
		}
		text, err := toString(raw)
		if err != nil {
			return nil, err
		}
		return FormatDatetime(text), nil
	case t == jdbc.TimeWithTimezone || (t == jdbc.Time && typeName == "timetz"):
		text, err := toString(raw)
		if err != nil {
			return nil, err
		}
		return ConvertOffsetTimeToUTC(text)
	case t == jdbc.Time:
		text, err := toString(raw)
		if err != nil {
			return nil, err
		}
		return FormatTime(text), nil
	case t == jdbc.Array:
		text, err := toString(raw)
		if err != nil {
			return nil, err
		}
		return convertArray(col, text)
	default:
		return toString(raw)
	}
}

// convertArray decodes a PostgreSQL array literal such as {1,2,3} and converts
// every element with the array's element type.
func convertArray(col Column, literal string) (any, error) {
	nodes, err := parsePGArray(literal)
	if err != nil {
		return nil, err
	}

	elementName := jdbc.ElementTypeName(col.DatabaseTypeName)
	elementCol := NewColumn(col.Engine, elementName)
	return convertArrayNodes(elementCol, nodes)
}

func convertArrayNodes(elementCol Column, nodes []pgArrayNode) ([]any, error) {
	out := make([]any, 0, len(nodes))
	for _, node := range nodes {
		switch {
		case node.isArray:
			nested, err := convertArrayNodes(elementCol, node.children)
			if err != nil {
				return nil, err
			}
			out = append(out, nested)
		case node.value == nil:
			out = append(out, nil)
		default:
			value, err := ConvertValue(elementCol, *node.value)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
		}
	}
	return out, nil
}

func toInt64(raw any) (any, error) {
	switch v := raw.(type) {
	case int64:
		return v, nil
	case int32:
		return int64(v), nil
	case int:
		return int64(v), nil
	case uint64:
		return int64(v), nil
	case float64:
		return int64(v), nil
	case bool:
		if v {
			return int64(1), nil
		}
		return int64(0), nil
	case []byte:
		return parseInt64(string(v))
	case string:
		return parseInt64(v)
	default:
		return nil, fmt.Errorf("cannot read %T as a long", raw)
	}
}

func parseInt64(text string) (any, error) {
	trimmed := strings.TrimSpace(text)
	if parsed, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return parsed, nil
	}
	// Unsigned BIGINT columns can overflow int64; fall back to the text form
	// rather than reporting a wrong number.
	if parsed, err := strconv.ParseUint(trimmed, 10, 64); err == nil {
		return strconv.FormatUint(parsed, 10), nil
	}
	if parsed, err := strconv.ParseFloat(trimmed, 64); err == nil {
		return int64(parsed), nil
	}
	return nil, fmt.Errorf("cannot read %q as a long", text)
}

func toFloat64(raw any) (any, error) {
	switch v := raw.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case []byte:
		return parseFloat64(string(v))
	case string:
		return parseFloat64(v)
	default:
		return nil, fmt.Errorf("cannot read %T as a double", raw)
	}
}

func parseFloat64(text string) (any, error) {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil {
		return nil, fmt.Errorf("cannot read %q as a double", text)
	}
	return parsed, nil
}

func toBool(raw any) (any, error) {
	switch v := raw.(type) {
	case bool:
		return v, nil
	case int64:
		return v != 0, nil
	case float64:
		return v != 0, nil
	case []byte:
		// MySQL sends BIT columns as raw bytes.
		if len(v) == 1 && (v[0] == 0 || v[0] == 1) {
			return v[0] == 1, nil
		}
		return parseBool(string(v))
	case string:
		return parseBool(v)
	default:
		return nil, fmt.Errorf("cannot read %T as a boolean", raw)
	}
}

func parseBool(text string) (any, error) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "t", "true", "1", "y", "yes", "on":
		return true, nil
	case "f", "false", "0", "n", "no", "off", "":
		return false, nil
	}
	if parsed, err := strconv.ParseFloat(strings.TrimSpace(text), 64); err == nil {
		return parsed != 0, nil
	}
	return nil, fmt.Errorf("cannot read %q as a boolean", text)
}

func toBytes(raw any) ([]byte, error) {
	switch v := raw.(type) {
	case []byte:
		return v, nil
	case string:
		return []byte(v), nil
	default:
		text, err := toString(raw)
		if err != nil {
			return nil, fmt.Errorf("cannot read %T as a blob", raw)
		}
		return []byte(text), nil
	}
}

func toString(raw any) (string, error) {
	switch v := raw.(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64), nil
	case bool:
		return strconv.FormatBool(v), nil
	case time.Time:
		return StripNanoSecs(v.Format(datetimeLayout)), nil
	default:
		return fmt.Sprint(raw), nil
	}
}
