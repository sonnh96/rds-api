package jdbc

import "testing"

func TestTypeFromNameMySQL(t *testing.T) {
	tests := []struct {
		name string
		want int
	}{
		{"INT", Integer},
		{"int", Integer},
		{"BIGINT", BigInt},
		{"TINYINT", TinyInt},
		{"SMALLINT", SmallInt},
		{"MEDIUMINT", Integer},
		{"FLOAT", Real},
		{"DOUBLE", Double},
		{"DECIMAL", Decimal},
		{"VARCHAR", VarChar},
		{"TEXT", LongVarChar},
		{"JSON", LongVarChar},
		{"BLOB", LongVarBinary},
		{"BINARY", Binary},
		{"VARBINARY", VarBinary},
		{"DATE", Date},
		{"TIME", Time},
		{"DATETIME", Timestamp},
		{"TIMESTAMP", Timestamp},
		{"BIT", Bit},
		{"UNSIGNED BIGINT", BigInt},
		{"SOMETHING_NEW", Other},
		{"", Other},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := TypeFromName("mysql", tc.name); got != tc.want {
				t.Errorf("TypeFromName(mysql, %q) = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestTypeFromNamePostgres(t *testing.T) {
	tests := []struct {
		name string
		want int
	}{
		{"INT2", SmallInt},
		{"INT4", Integer},
		{"INT8", BigInt},
		{"FLOAT4", Real},
		{"FLOAT8", Double},
		{"NUMERIC", Numeric},
		{"BOOL", Boolean},
		{"TEXT", VarChar},
		{"VARCHAR", VarChar},
		{"BYTEA", VarBinary},
		{"UUID", Other},
		{"JSONB", Other},
		{"DATE", Date},
		{"TIME", Time},
		{"TIMETZ", TimeWithTimezone},
		{"TIMESTAMP", Timestamp},
		{"TIMESTAMPTZ", TimestampWithTimezone},
		{"_INT4", Array},
		{"_TEXT", Array},
		{"SOMETHING_NEW", Other},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := TypeFromName("postgres", tc.name); got != tc.want {
				t.Errorf("TypeFromName(postgres, %q) = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestElementTypeName(t *testing.T) {
	if got, want := ElementTypeName("_INT4"), "INT4"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := ElementTypeName("INT4"), "INT4"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The type families must match the JDBC-type-to-Data-API-type table in the AWS
// documentation.
func TestTypeFamilies(t *testing.T) {
	for _, tc := range []struct {
		name    string
		matches func(int) bool
		members []int
		others  []int
	}{
		{"long", IsLong, []int{Integer, TinyInt, SmallInt, BigInt}, []int{Double, Boolean, VarChar}},
		{"double", IsDouble, []int{Float, Real, Double}, []int{Integer, Decimal, Numeric}},
		{"boolean", IsBoolean, []int{Boolean, Bit}, []int{TinyInt, Integer}},
		{"blob", IsBlob, []int{Blob, Binary, LongVarBinary, VarBinary}, []int{VarChar, Clob}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, member := range tc.members {
				if !tc.matches(member) {
					t.Errorf("type %d should be in the %s family", member, tc.name)
				}
			}
			for _, other := range tc.others {
				if tc.matches(other) {
					t.Errorf("type %d should not be in the %s family", other, tc.name)
				}
			}
		})
	}
}
