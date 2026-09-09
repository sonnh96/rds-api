package models

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestFieldFromValue(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{"nil", nil, `{"isNull":true}`},
		{"string", "hello", `{"stringValue":"hello"}`},
		{"long", int64(7), `{"longValue":7}`},
		{"double", 1.5, `{"doubleValue":1.5}`},
		{"boolean", true, `{"booleanValue":true}`},
		{"blob", BlobFromBytes([]byte("hello")), `{"blobValue":"aGVsbG8="}`},
		{"array", []any{int64(1), int64(2)}, `{"arrayValue":{"longValues":[1,2]}}`},
		{"unsupported type", struct{}{}, `{"isNull":true}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(FieldFromValue(tc.value))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(encoded) != tc.want {
				t.Errorf("got %s, want %s", encoded, tc.want)
			}
		})
	}
}

func TestArrayValueFromSlice(t *testing.T) {
	tests := []struct {
		name   string
		values []any
		want   string
	}{
		{"strings", []any{"a", "b"}, `{"stringValues":["a","b"]}`},
		{"booleans", []any{true, false}, `{"booleanValues":[true,false]}`},
		{"doubles", []any{1.5, 2.5}, `{"doubleValues":[1.5,2.5]}`},
		{"longs", []any{int64(1), int64(2)}, `{"longValues":[1,2]}`},
		{
			"nested",
			[]any{[]any{int64(1)}, []any{int64(2)}},
			`{"arrayValues":[{"longValues":[1]},{"longValues":[2]}]}`,
		},
		{"empty", []any{}, `{}`},
		{"leading null does not decide the type", []any{nil, "a"}, `{"stringValues":["a"]}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(ArrayValueFromSlice(tc.values))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(encoded) != tc.want {
				t.Errorf("got %s, want %s", encoded, tc.want)
			}
		})
	}
}

func TestSqlParameterCastValue(t *testing.T) {
	long := int64(9)
	double := 2.5
	boolean := true
	text := "hello"
	blob := "aGVsbG8="
	isNull := true

	tests := []struct {
		name  string
		param SqlParameter
		want  any
	}{
		{"long", SqlParameter{Name: "p", Value: Field{LongValue: &long}}, int64(9)},
		{"double", SqlParameter{Name: "p", Value: Field{DoubleValue: &double}}, 2.5},
		{"boolean", SqlParameter{Name: "p", Value: Field{BooleanValue: &boolean}}, true},
		{"string", SqlParameter{Name: "p", Value: Field{StringValue: &text}}, "hello"},
		{"blob", SqlParameter{Name: "p", Value: Field{BlobValue: &blob}}, []byte("hello")},
		{"isNull", SqlParameter{Name: "p", Value: Field{IsNull: &isNull}}, nil},
		{"empty field", SqlParameter{Name: "p", Value: Field{}}, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.param.CastValue()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("CastValue() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestSqlParameterCastValueTypeHints(t *testing.T) {
	valid := []struct {
		hint  string
		value string
	}{
		{"DATE", "2021-03-10"},
		{"TIME", "22:41:04"},
		{"TIME", "22:41:04.123"},
		{"TIMESTAMP", "2021-03-10 22:41:04"},
		{"TIMESTAMP", "2021-03-10 22:41:04.123456"},
		{"DECIMAL", "1.25"},
		{"UUID", "123e4567-e89b-12d3-a456-426614174000"},
		{"JSON", `{"a":1}`},
	}
	for _, tc := range valid {
		t.Run(tc.hint+"/"+tc.value, func(t *testing.T) {
			value := tc.value
			param := SqlParameter{Name: "p", Value: Field{StringValue: &value}, TypeHint: tc.hint}
			got, err := param.CastValue()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.value {
				t.Errorf("CastValue() = %v, want %v", got, tc.value)
			}
		})
	}

	invalid := []struct {
		hint  string
		value string
	}{
		{"DATE", "10-03-2021"},
		{"DATE", "2021-13-45"},
		{"TIME", "not a time"},
		{"TIMESTAMP", "2021-03-10"},
		{"DECIMAL", "abc"},
		{"UUID", "not-a-uuid"},
		{"NONSENSE", "x"},
	}
	for _, tc := range invalid {
		t.Run("invalid/"+tc.hint+"/"+tc.value, func(t *testing.T) {
			value := tc.value
			param := SqlParameter{Name: "p", Value: Field{StringValue: &value}, TypeHint: tc.hint}
			if _, err := param.CastValue(); err == nil {
				t.Errorf("expected an error for %s %q", tc.hint, tc.value)
			}
		})
	}
}

func TestSqlParameterCastValueRejectsArrays(t *testing.T) {
	param := SqlParameter{Name: "p", Value: Field{ArrayValue: &ArrayValue{LongValues: []int64{1}}}}

	_, err := param.CastValue()
	if err == nil {
		t.Fatal("expected an error for an array parameter")
	}
	if want := "Array parameters are not supported."; err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestSqlParameterCastValueRejectsInvalidBase64(t *testing.T) {
	value := "not base64!!"
	param := SqlParameter{Name: "p", Value: Field{BlobValue: &value}}

	if _, err := param.CastValue(); err == nil {
		t.Fatal("expected an error for an invalid base64 blob")
	}
}

func TestExecuteStatementResponseJSON(t *testing.T) {
	tests := []struct {
		name     string
		response ExecuteStatementResponse
		want     string
	}{
		{
			name:     "an update carries generatedFields but no records",
			response: ExecuteStatementResponse{NumberOfRecordsUpdated: 1}.WithGeneratedFields([]Field{LongField(3)}),
			want:     `{"numberOfRecordsUpdated":1,"generatedFields":[{"longValue":3}]}`,
		},
		{
			name:     "an update without a generated key still carries an empty list",
			response: ExecuteStatementResponse{NumberOfRecordsUpdated: 2}.WithGeneratedFields(nil),
			want:     `{"numberOfRecordsUpdated":2,"generatedFields":[]}`,
		},
		{
			name:     "a query carries records but no generatedFields",
			response: ExecuteStatementResponse{}.WithRecords([][]Field{{StringField("a")}}),
			want:     `{"numberOfRecordsUpdated":0,"records":[[{"stringValue":"a"}]]}`,
		},
		{
			name:     "a query with no rows still carries an empty list",
			response: ExecuteStatementResponse{}.WithRecords(nil),
			want:     `{"numberOfRecordsUpdated":0,"records":[]}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.response)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(encoded) != tc.want {
				t.Errorf("got %s, want %s", encoded, tc.want)
			}
		})
	}
}

func TestExecuteStatementRequestUnmarshalIgnoresUnknownFields(t *testing.T) {
	body := `{"resourceArn":"arn","secretArn":"secret","sql":"SELECT 1","somethingNew":true}`

	var request ExecuteStatementRequest
	if err := json.Unmarshal([]byte(body), &request); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if request.Sql != "SELECT 1" {
		t.Errorf("Sql = %q, want %q", request.Sql, "SELECT 1")
	}
}
