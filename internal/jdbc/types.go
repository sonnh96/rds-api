// Package jdbc reproduces the java.sql.Types constants and maps the type names
// reported by the Go database drivers onto them.
//
// The Data API describes result columns with JDBC type codes, and its rules for
// rendering a value are written in terms of them. Go's database/sql exposes a
// driver-specific type *name* instead, so this package translates those names
// into the corresponding integers. That lets the dataapi package express the
// conversion rules the way the AWS documentation does, and lets
// ColumnMetadata.type report the codes clients expect.
package jdbc

import "strings"

// java.sql.Types constants.
const (
	Bit                   = -7
	TinyInt               = -6
	SmallInt              = 5
	Integer               = 4
	BigInt                = -5
	Float                 = 6
	Real                  = 7
	Double                = 8
	Numeric               = 2
	Decimal               = 3
	Char                  = 1
	VarChar               = 12
	LongVarChar           = -1
	Date                  = 91
	Time                  = 92
	Timestamp             = 93
	Binary                = -2
	VarBinary             = -3
	LongVarBinary         = -4
	Null                  = 0
	Other                 = 1111
	JavaObject            = 2000
	Distinct              = 2001
	Struct                = 2002
	Array                 = 2003
	Blob                  = 2004
	Clob                  = 2005
	Ref                   = 2006
	DataLink              = 70
	Boolean               = 16
	RowID                 = -8
	NChar                 = -15
	NVarChar              = -9
	LongNVarChar          = -16
	NClob                 = 2011
	SQLXML                = 2009
	RefCursor             = 2012
	TimeWithTimezone      = 2013
	TimestampWithTimezone = 2014
)

// mysqlTypes maps the DatabaseTypeName values of github.com/go-sql-driver/mysql
// onto JDBC type codes.
var mysqlTypes = map[string]int{
	"BIT":        Bit,
	"TINYINT":    TinyInt,
	"SMALLINT":   SmallInt,
	"MEDIUMINT":  Integer,
	"INT":        Integer,
	"INTEGER":    Integer,
	"BIGINT":     BigInt,
	"YEAR":       SmallInt,
	"FLOAT":      Real,
	"DOUBLE":     Double,
	"DECIMAL":    Decimal,
	"NEWDECIMAL": Decimal,
	"CHAR":       Char,
	"VARCHAR":    VarChar,
	"TINYTEXT":   LongVarChar,
	"TEXT":       LongVarChar,
	"MEDIUMTEXT": LongVarChar,
	"LONGTEXT":   LongVarChar,
	"JSON":       LongVarChar,
	"ENUM":       VarChar,
	"SET":        VarChar,
	"BINARY":     Binary,
	"VARBINARY":  VarBinary,
	"TINYBLOB":   VarBinary,
	"BLOB":       LongVarBinary,
	"MEDIUMBLOB": LongVarBinary,
	"LONGBLOB":   LongVarBinary,
	"GEOMETRY":   Binary,
	"DATE":       Date,
	"NEWDATE":    Date,
	"TIME":       Time,
	"DATETIME":   Timestamp,
	"TIMESTAMP":  Timestamp,
	"NULL":       Null,
}

// postgresTypes maps the DatabaseTypeName values of github.com/lib/pq onto JDBC
// type codes. lib/pq reports the PostgreSQL type name, e.g. INT4 or TIMESTAMPTZ.
var postgresTypes = map[string]int{
	"BOOL":        Boolean,
	"BIT":         Bit,
	"VARBIT":      Bit,
	"INT2":        SmallInt,
	"INT4":        Integer,
	"INT8":        BigInt,
	"OID":         BigInt,
	"FLOAT4":      Real,
	"FLOAT8":      Double,
	"NUMERIC":     Numeric,
	"MONEY":       Decimal,
	"CHAR":        Char,
	"BPCHAR":      Char,
	"VARCHAR":     VarChar,
	"TEXT":        VarChar,
	"NAME":        VarChar,
	"UUID":        Other,
	"JSON":        Other,
	"JSONB":       Other,
	"XML":         SQLXML,
	"BYTEA":       VarBinary,
	"DATE":        Date,
	"TIME":        Time,
	"TIMETZ":      TimeWithTimezone,
	"TIMESTAMP":   Timestamp,
	"TIMESTAMPTZ": TimestampWithTimezone,
	"INTERVAL":    Other,
	"POINT":       Other,
	"LINE":        Other,
	"LSEG":        Other,
	"BOX":         Other,
	"PATH":        Other,
	"POLYGON":     Other,
	"CIRCLE":      Other,
	"CIDR":        Other,
	"INET":        Other,
	"MACADDR":     Other,
	"TSVECTOR":    Other,
	"RECORD":      Struct,
}

// TypeFromName resolves a driver type name to a JDBC type code. engine selects
// the driver's naming scheme ("mysql" or "postgres"). Unknown names fall back to
// Other, which the value conversion renders as a string, matching the JDBC
// "other types become STRING" rule.
func TypeFromName(engine, name string) int {
	upper := strings.ToUpper(strings.TrimSpace(name))
	if upper == "" {
		return Other
	}

	if engine == "postgres" {
		// lib/pq prefixes array types with an underscore, e.g. _INT4.
		if strings.HasPrefix(upper, "_") {
			return Array
		}
		if t, ok := postgresTypes[upper]; ok {
			return t
		}
		return Other
	}

	if t, ok := mysqlTypes[upper]; ok {
		return t
	}
	// The MySQL driver reports unsigned columns as e.g. "UNSIGNED BIGINT".
	if rest, ok := strings.CutPrefix(upper, "UNSIGNED "); ok {
		if t, ok := mysqlTypes[rest]; ok {
			return t
		}
	}
	return Other
}

// ElementTypeName strips the array marker from a PostgreSQL array type name,
// so that _INT4 becomes INT4.
func ElementTypeName(name string) string {
	return strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(name)), "_")
}

// IsLong reports whether values of this type are rendered as longValue.
func IsLong(t int) bool {
	return t == Integer || t == TinyInt || t == SmallInt || t == BigInt
}

// IsDouble reports whether values of this type are rendered as doubleValue.
func IsDouble(t int) bool {
	return t == Float || t == Real || t == Double
}

// IsBoolean reports whether values of this type are rendered as booleanValue.
func IsBoolean(t int) bool {
	return t == Boolean || t == Bit
}

// IsBlob reports whether values of this type are rendered as blobValue.
func IsBlob(t int) bool {
	return t == Blob || t == Binary || t == LongVarBinary || t == VarBinary
}

// IsSigned reports whether the type is a signed numeric type.
func IsSigned(t int) bool {
	return IsLong(t) || IsDouble(t) || t == Numeric || t == Decimal
}
