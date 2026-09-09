package dataapi

import (
	"encoding/json"
	"testing"
	"time"
)

func fieldJSON(t *testing.T, engine, typeName string, raw any) string {
	t.Helper()
	field, err := FieldFor(NewColumn(engine, typeName), raw)
	if err != nil {
		t.Fatalf("FieldFor(%s, %v) returned error: %v", typeName, raw, err)
	}
	encoded, err := json.Marshal(field)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return string(encoded)
}

// These cases follow the JDBC-type-to-Data-API-type table in the AWS docs, and
// cover the exact assertions the shell integration tests make.
func TestFieldForMySQL(t *testing.T) {
	tests := []struct {
		name     string
		typeName string
		raw      any
		want     string
	}{
		{"null", "VARCHAR", nil, `{"isNull":true}`},
		{"text", "VARCHAR", []byte("hello"), `{"stringValue":"hello"}`},
		{"int", "INT", int64(1), `{"longValue":1}`},
		{"bigint from a comparison", "BIGINT", int64(1), `{"longValue":1}`},
		{"tinyint", "TINYINT", int64(1), `{"longValue":1}`},
		{"double", "DOUBLE", 1.5, `{"doubleValue":1.5}`},
		{"float", "FLOAT", 1.5, `{"doubleValue":1.5}`},
		{"decimal stays a string", "DECIMAL", []byte("1.25"), `{"stringValue":"1.25"}`},
		{"bit is a boolean", "BIT", []byte{1}, `{"booleanValue":true}`},
		{"binary is a blob", "VARBINARY", []byte("hello"), `{"blobValue":"aGVsbG8="}`},
		{"blob", "BLOB", []byte("hello"), `{"blobValue":"aGVsbG8="}`},
		{"text is not a blob", "TEXT", []byte("hello"), `{"stringValue":"hello"}`},
		{"datetime", "DATETIME", []byte("2021-03-10 22:41:05"), `{"stringValue":"2021-03-10 22:41:05"}`},
		{"datetime trims zeros", "DATETIME", []byte("2021-03-10 22:41:05.000000"), `{"stringValue":"2021-03-10 22:41:05"}`},
		{"date", "DATE", []byte("2021-03-10"), `{"stringValue":"2021-03-10"}`},
		{"time", "TIME", []byte("22:41:04.123456"), `{"stringValue":"22:41:04.123"}`},
		{"json is a string", "JSON", []byte(`{"a":1}`), `{"stringValue":"{\"a\":1}"}`},
		{"unsigned bigint", "UNSIGNED BIGINT", []byte("18446744073709551615"), `{"stringValue":"18446744073709551615"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := fieldJSON(t, "mysql", tc.typeName, tc.raw); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestFieldForPostgres(t *testing.T) {
	tz := time.FixedZone("+02", 2*60*60)

	tests := []struct {
		name     string
		typeName string
		raw      any
		want     string
	}{
		{"null", "TEXT", nil, `{"isNull":true}`},
		{"text", "TEXT", "hello", `{"stringValue":"hello"}`},
		{"int4", "INT4", int64(1), `{"longValue":1}`},
		{"int8", "INT8", int64(1), `{"longValue":1}`},
		{"float8", "FLOAT8", 1.5, `{"doubleValue":1.5}`},
		{"numeric stays a string", "NUMERIC", []byte("1.25"), `{"stringValue":"1.25"}`},
		{"bool", "BOOL", true, `{"booleanValue":true}`},
		{"bytea is a blob", "BYTEA", []byte("hello"), `{"blobValue":"aGVsbG8="}`},
		{"uuid is a string", "UUID", []byte("123e4567-e89b-12d3-a456-426614174000"), `{"stringValue":"123e4567-e89b-12d3-a456-426614174000"}`},
		{"jsonb is a string", "JSONB", []byte(`{"a": 1}`), `{"stringValue":"{\"a\": 1}"}`},
		{
			"timestamptz becomes UTC",
			"TIMESTAMPTZ",
			time.Date(2021, 3, 10, 22, 41, 4, 68123000, tz),
			`{"stringValue":"2021-03-10 20:41:04.068123"}`,
		},
		{
			"timestamptz from text becomes UTC",
			"TIMESTAMPTZ",
			[]byte("2021-03-10 22:41:04.068123+02"),
			`{"stringValue":"2021-03-10 20:41:04.068123"}`,
		},
		{
			"timestamp keeps its wall clock",
			"TIMESTAMP",
			time.Date(2021, 3, 10, 22, 41, 4, 68123000, time.UTC),
			`{"stringValue":"2021-03-10 22:41:04.068123"}`,
		},
		{"timetz becomes UTC", "TIMETZ", []byte("22:41:04.123456+02"), `{"stringValue":"20:41:04.123"}`},
		{"time", "TIME", []byte("22:41:04.123456"), `{"stringValue":"22:41:04.123"}`},
		{"date", "DATE", []byte("2021-03-10"), `{"stringValue":"2021-03-10"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := fieldJSON(t, "postgres", tc.typeName, tc.raw); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestFieldForPostgresArrays(t *testing.T) {
	tests := []struct {
		name     string
		typeName string
		raw      any
		want     string
	}{
		{"int array", "_INT4", []byte("{1,2,3}"), `{"arrayValue":{"longValues":[1,2,3]}}`},
		{"text array", "_TEXT", []byte(`{a,b}`), `{"arrayValue":{"stringValues":["a","b"]}}`},
		{"quoted text array", "_TEXT", []byte(`{"Volume","Material"}`), `{"arrayValue":{"stringValues":["Volume","Material"]}}`},
		{"text array with a comma inside an element", "_TEXT", []byte(`{"a,b",c}`), `{"arrayValue":{"stringValues":["a,b","c"]}}`},
		{"bool array", "_BOOL", []byte("{t,f}"), `{"arrayValue":{"booleanValues":[true,false]}}`},
		{"float array", "_FLOAT8", []byte("{1.5,2.5}"), `{"arrayValue":{"doubleValues":[1.5,2.5]}}`},
		{"nested array", "_INT4", []byte("{{1,2},{3,4}}"), `{"arrayValue":{"arrayValues":[{"longValues":[1,2]},{"longValues":[3,4]}]}}`},
		{"empty array", "_INT4", []byte("{}"), `{"arrayValue":{}}`},
		{"array with a null element", "_TEXT", []byte(`{a,NULL}`), `{"arrayValue":{"stringValues":["a"]}}`},
		{"quoted NULL is a string", "_TEXT", []byte(`{"NULL"}`), `{"arrayValue":{"stringValues":["NULL"]}}`},
		{"escaped quote inside an element", "_TEXT", []byte(`{"a\"b"}`), `{"arrayValue":{"stringValues":["a\"b"]}}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := fieldJSON(t, "postgres", tc.typeName, tc.raw); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestConvertValueReportsBadArrayLiteral(t *testing.T) {
	if _, err := ConvertValue(NewColumn("postgres", "_INT4"), "not an array"); err == nil {
		t.Fatal("expected an error for a malformed array literal")
	}
}

func TestConvertValueBooleanForms(t *testing.T) {
	tests := []struct {
		raw  any
		want bool
	}{
		{true, true},
		{false, false},
		{int64(1), true},
		{int64(0), false},
		{[]byte{1}, true},
		{[]byte{0}, false},
		{"t", true},
		{"f", false},
		{"true", true},
		{"false", false},
	}

	for _, tc := range tests {
		got, err := ConvertValue(NewColumn("postgres", "BOOL"), tc.raw)
		if err != nil {
			t.Fatalf("ConvertValue(%#v) returned error: %v", tc.raw, err)
		}
		if got != tc.want {
			t.Errorf("ConvertValue(%#v) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestFieldForUnknownTypeFallsBackToString(t *testing.T) {
	if got, want := fieldJSON(t, "postgres", "SOMETHING_NEW", []byte("x")), `{"stringValue":"x"}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// A field built from an unset value must survive a round trip as isNull.
func TestFieldForNullOfEveryFamily(t *testing.T) {
	for _, typeName := range []string{"INT4", "FLOAT8", "BOOL", "BYTEA", "TIMESTAMPTZ", "TIMESTAMP", "TIMETZ", "TIME", "TEXT", "_INT4"} {
		if got, want := fieldJSON(t, "postgres", typeName, nil), `{"isNull":true}`; got != want {
			t.Errorf("%s: got %s, want %s", typeName, got, want)
		}
	}
}
