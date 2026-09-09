package resource

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/sonnh96/local-data-api/internal/models"
	"github.com/sonnh96/local-data-api/internal/testdb"
)

func openResource(t *testing.T, engine Engine, fake *testdb.DB) *Resource {
	t.Helper()

	manager := NewManager()
	manager.SetOpener(fake.Opener())
	manager.SetResource(engine, "arn:resource", "127.0.0.1", 3306, nil)
	t.Cleanup(func() { manager.Close() })

	res, err := manager.GetResource(context.Background(), "arn:resource", "root", "example", "", "test", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return res
}

func stringParam(name, value string) models.SqlParameter {
	return models.SqlParameter{Name: name, Value: models.Field{StringValue: &value}}
}

func TestExecuteQueryBuildsRecords(t *testing.T) {
	fake := testdb.New()
	fake.OnQuery = func(string, []driver.Value) (*testdb.ResultSet, error) {
		return &testdb.ResultSet{
			Columns: []testdb.Column{
				{Name: "id", TypeName: "BIGINT"},
				{Name: "name", TypeName: "VARCHAR"},
				{Name: "age", TypeName: "INT"},
			},
			Rows: [][]driver.Value{
				{int64(1), []byte("ichiro"), int64(17)},
				{int64(2), []byte("ken"), nil},
			},
		}, nil
	}

	res := openResource(t, MySQL, fake)
	response, err := res.Execute(context.Background(), "SELECT * FROM users", nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	encoded, _ := json.Marshal(response)
	want := `{"numberOfRecordsUpdated":0,"records":[[{"longValue":1},{"stringValue":"ichiro"},{"longValue":17}],[{"longValue":2},{"stringValue":"ken"},{"isNull":true}]]}`
	if string(encoded) != want {
		t.Errorf("got  %s\nwant %s", encoded, want)
	}
}

func TestExecuteQueryWithoutRows(t *testing.T) {
	fake := testdb.New()
	fake.OnQuery = func(string, []driver.Value) (*testdb.ResultSet, error) {
		return &testdb.ResultSet{Columns: []testdb.Column{{Name: "id", TypeName: "BIGINT"}}}, nil
	}

	res := openResource(t, MySQL, fake)
	response, err := res.Execute(context.Background(), "SELECT * FROM users", nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	encoded, _ := json.Marshal(response)
	if want := `{"numberOfRecordsUpdated":0,"records":[]}`; string(encoded) != want {
		t.Errorf("got %s, want %s", encoded, want)
	}
}

func TestExecuteQueryIncludesColumnMetadata(t *testing.T) {
	fake := testdb.New()
	fake.OnQuery = func(string, []driver.Value) (*testdb.ResultSet, error) {
		return &testdb.ResultSet{
			Columns: []testdb.Column{{Name: "value", TypeName: "BIGINT", Nullable: true, HasNullable: true}},
			Rows:    [][]driver.Value{{int64(1)}},
		}, nil
	}

	res := openResource(t, MySQL, fake)
	response, err := res.Execute(context.Background(), "SELECT 1 AS value", nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(response.ColumnMetadata) != 1 {
		t.Fatalf("got %d columns of metadata, want 1", len(response.ColumnMetadata))
	}
	metadata := response.ColumnMetadata[0]
	if metadata.Name != "value" || metadata.Label != "value" {
		t.Errorf("name/label = %q/%q", metadata.Name, metadata.Label)
	}
	if metadata.TypeName != "BIGINT" {
		t.Errorf("typeName = %q, want BIGINT", metadata.TypeName)
	}
	if metadata.Type != -5 { // java.sql.Types.BIGINT
		t.Errorf("type = %d, want -5", metadata.Type)
	}
	if metadata.Nullable != 1 {
		t.Errorf("nullable = %d, want 1", metadata.Nullable)
	}
	if !metadata.IsSigned {
		t.Error("a BIGINT column should report isSigned")
	}
}

func TestExecuteQueryOmitsColumnMetadataByDefault(t *testing.T) {
	fake := testdb.New()
	fake.OnQuery = func(string, []driver.Value) (*testdb.ResultSet, error) {
		return &testdb.ResultSet{
			Columns: []testdb.Column{{Name: "value", TypeName: "BIGINT"}},
			Rows:    [][]driver.Value{{int64(1)}},
		}, nil
	}

	res := openResource(t, MySQL, fake)
	response, err := res.Execute(context.Background(), "SELECT 1 AS value", nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if response.ColumnMetadata != nil {
		t.Errorf("columnMetadata = %v, want nil", response.ColumnMetadata)
	}
}

func TestExecuteUpdateReportsRowsAndGeneratedKey(t *testing.T) {
	fake := testdb.New()
	fake.OnExec = func(string, []driver.Value) (*testdb.ExecResult, error) {
		return &testdb.ExecResult{LastInsertID: 3, RowsAffected: 1}, nil
	}

	res := openResource(t, MySQL, fake)
	response, err := res.Execute(context.Background(), "INSERT INTO users (name) VALUES ('a')", nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	encoded, _ := json.Marshal(response)
	if want := `{"numberOfRecordsUpdated":1,"generatedFields":[{"longValue":3}]}`; string(encoded) != want {
		t.Errorf("got %s, want %s", encoded, want)
	}
}

// PostgreSQL has no LAST_INSERT_ID, and its driver reports an error rather than
// a key, which must leave generatedFields empty instead of failing the request.
func TestExecuteUpdateWithoutGeneratedKey(t *testing.T) {
	fake := testdb.New()
	fake.OnExec = func(string, []driver.Value) (*testdb.ExecResult, error) {
		return &testdb.ExecResult{RowsAffected: 2, LastInsertIDErr: errors.New("unsupported")}, nil
	}

	res := openResource(t, Postgres, fake)
	response, err := res.Execute(context.Background(), "UPDATE users SET name = 'a'", nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	encoded, _ := json.Marshal(response)
	if want := `{"numberOfRecordsUpdated":2,"generatedFields":[]}`; string(encoded) != want {
		t.Errorf("got %s, want %s", encoded, want)
	}
}

func TestExecuteBindsNamedParameters(t *testing.T) {
	fake := testdb.New()
	fake.OnExec = func(string, []driver.Value) (*testdb.ExecResult, error) {
		return &testdb.ExecResult{RowsAffected: 1}, nil
	}

	res := openResource(t, MySQL, fake)
	_, err := res.Execute(
		context.Background(),
		"INSERT INTO users (name, nickname) VALUES (:name, :name)",
		[]models.SqlParameter{stringParam("name", "ichiro")},
		false,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := fake.Calls()
	if len(calls) != 1 {
		t.Fatalf("ran %d statements, want 1", len(calls))
	}
	if want := "INSERT INTO users (name, nickname) VALUES (?, ?)"; calls[0].Query != want {
		t.Errorf("query = %q, want %q", calls[0].Query, want)
	}
	if want := []driver.Value{"ichiro", "ichiro"}; !reflect.DeepEqual(calls[0].Args, want) {
		t.Errorf("args = %v, want %v", calls[0].Args, want)
	}
}

func TestExecuteUsesDollarPlaceholdersOnPostgres(t *testing.T) {
	fake := testdb.New()
	fake.OnExec = func(string, []driver.Value) (*testdb.ExecResult, error) {
		return &testdb.ExecResult{RowsAffected: 1}, nil
	}

	res := openResource(t, Postgres, fake)
	_, err := res.Execute(
		context.Background(),
		"INSERT INTO users (name, nickname) VALUES (:name, :name)",
		[]models.SqlParameter{stringParam("name", "ichiro")},
		false,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := fake.Calls()
	if want := "INSERT INTO users (name, nickname) VALUES ($1, $1)"; calls[0].Query != want {
		t.Errorf("query = %q, want %q", calls[0].Query, want)
	}
	if want := []driver.Value{"ichiro"}; !reflect.DeepEqual(calls[0].Args, want) {
		t.Errorf("args = %v, want %v", calls[0].Args, want)
	}
}

// Without parameters the statement is passed through untouched, so a colon in
// it cannot be mistaken for a bind parameter.
func TestExecuteWithoutParametersDoesNotRewrite(t *testing.T) {
	fake := testdb.New()
	res := openResource(t, MySQL, fake)

	_, err := res.Execute(context.Background(), "SELECT 'a:b' AS value", nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "SELECT 'a:b' AS value"; fake.Calls()[0].Query != want {
		t.Errorf("query = %q, want %q", fake.Calls()[0].Query, want)
	}
}

func TestExecuteReportsAMissingParameter(t *testing.T) {
	fake := testdb.New()
	res := openResource(t, MySQL, fake)

	_, err := res.Execute(
		context.Background(),
		"SELECT :missing",
		[]models.SqlParameter{stringParam("other", "x")},
		false,
	)
	if err == nil {
		t.Fatal("expected an error for the missing parameter")
	}
	if want := "Cannot find parameter: missing"; err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestBatchExecuteRunsOncePerParameterSet(t *testing.T) {
	fake := testdb.New()
	id := int64(0)
	fake.OnExec = func(string, []driver.Value) (*testdb.ExecResult, error) {
		id++
		return &testdb.ExecResult{LastInsertID: id, RowsAffected: 1}, nil
	}

	res := openResource(t, MySQL, fake)
	response, err := res.BatchExecute(context.Background(), "INSERT INTO users (name) VALUES (:name)", [][]models.SqlParameter{
		{stringParam("name", "ichiro")},
		{stringParam("name", "ken")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	encoded, _ := json.Marshal(response)
	want := `{"updateResults":[{"generatedFields":[{"longValue":1}]},{"generatedFields":[{"longValue":2}]}]}`
	if string(encoded) != want {
		t.Errorf("got  %s\nwant %s", encoded, want)
	}

	calls := fake.Calls()
	if len(calls) != 2 {
		t.Fatalf("ran %d statements, want 2", len(calls))
	}
	for i, wantArg := range []string{"ichiro", "ken"} {
		if want := []driver.Value{wantArg}; !reflect.DeepEqual(calls[i].Args, want) {
			t.Errorf("call %d args = %v, want %v", i, calls[i].Args, want)
		}
	}
}

func TestBatchExecuteWithoutParameterSets(t *testing.T) {
	fake := testdb.New()
	res := openResource(t, MySQL, fake)

	response, err := res.BatchExecute(context.Background(), "INSERT INTO users (name) VALUES (:name)", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	encoded, _ := json.Marshal(response)
	if want := `{"updateResults":[]}`; string(encoded) != want {
		t.Errorf("got %s, want %s", encoded, want)
	}
	if got := len(fake.Calls()); got != 0 {
		t.Errorf("ran %d statements, want 0", got)
	}
}

func TestExecuteWrapsDriverErrors(t *testing.T) {
	fake := testdb.New()
	fake.OnQuery = func(string, []driver.Value) (*testdb.ResultSet, error) {
		return nil, errors.New("Table 'test.nope' doesn't exist")
	}

	res := openResource(t, MySQL, fake)
	_, err := res.Execute(context.Background(), "SELECT * FROM nope", nil, false)
	if err == nil {
		t.Fatal("expected an error")
	}
	if want := "Table 'test.nope' doesn't exist"; err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}
