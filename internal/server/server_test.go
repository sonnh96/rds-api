package server

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sonnh96/local-data-api/internal/models"
	"github.com/sonnh96/local-data-api/internal/resource"
	"github.com/sonnh96/local-data-api/internal/secrets"
	"github.com/sonnh96/local-data-api/internal/testdb"
)

const (
	resourceArn = "arn:aws:rds:us-east-1:123456789012:cluster:dummy"
	secretArn   = "arn:aws:secretsmanager:us-east-1:123456789012:secret:dummy"
)

// newTestServer wires a server onto a scripted database. The returned *sql.DB
// lets a test check that every request handed its connection back to the pool.
func newTestServer(t *testing.T, engine resource.Engine, fake *testdb.DB) (http.Handler, *sql.DB) {
	t.Helper()

	pooled := fake.Open()
	resources := resource.NewManager()
	resources.SetOpener(func(string, string) (*sql.DB, error) { return pooled, nil })
	resources.SetResource(engine, resourceArn, "127.0.0.1", 3306, nil)
	t.Cleanup(func() { resources.Close() })

	secretManager := secrets.NewManager()
	secretManager.SetSecret("root", "example", secretArn)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(resources, secretManager, logger).Handler(), pooled
}

// assertNoLeakedConnections checks that no connection is still checked out.
func assertNoLeakedConnections(t *testing.T, pooled *sql.DB) {
	t.Helper()
	if inUse := pooled.Stats().InUse; inUse != 0 {
		t.Errorf("%d connections still checked out of the pool, want 0", inUse)
	}
}

func post(t *testing.T, handler http.Handler, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()

	var payload []byte
	switch typed := body.(type) {
	case string:
		payload = []byte(typed)
	default:
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		payload = encoded
	}

	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(payload)))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	decoded := map[string]any{}
	if recorder.Body.Len() > 0 {
		if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("cannot decode response %q: %v", recorder.Body.String(), err)
		}
	}
	return recorder, decoded
}

func TestExecuteSqlIsNotImplemented(t *testing.T) {
	handler, _ := newTestServer(t, resource.MySQL, testdb.New())

	recorder, body := post(t, handler, "/ExecuteSql", map[string]any{
		"awsSecretStoreArn":      secretArn,
		"dbClusterOrInstanceArn": resourceArn,
		"sqlStatements":          "SELECT 1",
	})

	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
	if body["code"] != "NotFoundException" || body["message"] != "NotImplemented" {
		t.Errorf("body = %v", body)
	}
}

func TestExecuteReturnsRecords(t *testing.T) {
	fake := testdb.New()
	fake.OnQuery = func(string, []driver.Value) (*testdb.ResultSet, error) {
		return &testdb.ResultSet{
			Columns: []testdb.Column{{Name: "value", TypeName: "VARCHAR"}},
			Rows:    [][]driver.Value{{[]byte("hello")}},
		}, nil
	}
	handler, _ := newTestServer(t, resource.MySQL, fake)

	recorder, body := post(t, handler, "/Execute", models.ExecuteStatementRequest{
		ResourceArn: resourceArn,
		SecretArn:   secretArn,
		Database:    "test",
		Sql:         "SELECT 'hello' AS value",
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body %v", recorder.Code, body)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}

	records, ok := body["records"].([]any)
	if !ok || len(records) != 1 {
		t.Fatalf("records = %v", body["records"])
	}
	first := records[0].([]any)[0].(map[string]any)
	if first["stringValue"] != "hello" {
		t.Errorf("stringValue = %v, want hello", first["stringValue"])
	}
}

// A statement sent outside a transaction is committed and its connection
// released before the response is written.
func TestExecuteCommitsAndClosesWhenNotInATransaction(t *testing.T) {
	fake := testdb.New()
	fake.OnExec = func(string, []driver.Value) (*testdb.ExecResult, error) {
		return &testdb.ExecResult{RowsAffected: 1, LastInsertID: 5}, nil
	}
	handler, pooled := newTestServer(t, resource.MySQL, fake)

	recorder, body := post(t, handler, "/Execute", models.ExecuteStatementRequest{
		ResourceArn: resourceArn,
		SecretArn:   secretArn,
		Database:    "test",
		Sql:         "INSERT INTO users (name) VALUES ('a')",
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body %v", recorder.Code, body)
	}
	if body["numberOfRecordsUpdated"] != float64(1) {
		t.Errorf("numberOfRecordsUpdated = %v, want 1", body["numberOfRecordsUpdated"])
	}
	if fake.Count("commit") != 1 {
		t.Errorf("committed %d times, want 1", fake.Count("commit"))
	}
	assertNoLeakedConnections(t, pooled)
}

func TestExecuteReportsAnUnknownSecret(t *testing.T) {
	handler, _ := newTestServer(t, resource.MySQL, testdb.New())

	recorder, body := post(t, handler, "/Execute", models.ExecuteStatementRequest{
		ResourceArn: resourceArn,
		SecretArn:   "arn:missing",
		Sql:         "SELECT 1",
	})

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", recorder.Code)
	}
	if body["code"] != "BadRequestException" {
		t.Errorf("code = %v", body["code"])
	}
}

func TestExecuteReportsAnUnknownResource(t *testing.T) {
	handler, _ := newTestServer(t, resource.MySQL, testdb.New())

	recorder, body := post(t, handler, "/Execute", models.ExecuteStatementRequest{
		ResourceArn: "arn:missing",
		SecretArn:   secretArn,
		Sql:         "SELECT 1",
	})

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", recorder.Code)
	}
	if want := "HttpEndPoint is not enabled for arn:missing"; body["message"] != want {
		t.Errorf("message = %v, want %v", body["message"], want)
	}
}

func TestExecuteReportsADatabaseError(t *testing.T) {
	fake := testdb.New()
	fake.OnQuery = func(string, []driver.Value) (*testdb.ResultSet, error) {
		return nil, errors.New("Table 'test.nope' doesn't exist")
	}
	handler, pooled := newTestServer(t, resource.MySQL, fake)

	recorder, body := post(t, handler, "/Execute", models.ExecuteStatementRequest{
		ResourceArn: resourceArn,
		SecretArn:   secretArn,
		Sql:         "SELECT * FROM nope",
	})

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", recorder.Code)
	}
	if body["code"] != "BadRequestException" {
		t.Errorf("code = %v", body["code"])
	}
	if !strings.Contains(body["message"].(string), "doesn't exist") {
		t.Errorf("message = %v", body["message"])
	}
	// A failed statement must not leak its connection.
	assertNoLeakedConnections(t, pooled)
	if fake.Count("commit") != 0 {
		t.Errorf("committed %d times, want 0", fake.Count("commit"))
	}
}

func TestExecuteRejectsAMalformedBody(t *testing.T) {
	handler, _ := newTestServer(t, resource.MySQL, testdb.New())

	recorder, body := post(t, handler, "/Execute", "{not json")
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", recorder.Code)
	}
	if body["code"] != "BadRequestException" {
		t.Errorf("code = %v", body["code"])
	}
}

func TestTransactionLifecycle(t *testing.T) {
	fake := testdb.New()
	fake.OnExec = func(string, []driver.Value) (*testdb.ExecResult, error) {
		return &testdb.ExecResult{RowsAffected: 1}, nil
	}
	handler, pooled := newTestServer(t, resource.MySQL, fake)

	_, begun := post(t, handler, "/BeginTransaction", models.BeginTransactionRequest{
		ResourceArn: resourceArn,
		SecretArn:   secretArn,
		Database:    "test",
	})
	transactionID, ok := begun["transactionId"].(string)
	if !ok || len(transactionID) != 184 {
		t.Fatalf("transactionId = %v", begun["transactionId"])
	}

	// Statements inside the transaction neither commit nor close.
	for range 2 {
		recorder, body := post(t, handler, "/Execute", models.ExecuteStatementRequest{
			ResourceArn:   resourceArn,
			SecretArn:     secretArn,
			Database:      "test",
			TransactionID: transactionID,
			Sql:           "INSERT INTO users (name) VALUES ('a')",
		})
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, body %v", recorder.Code, body)
		}
	}
	if fake.Count("commit") != 0 {
		t.Errorf("committed %d times before the commit request, want 0", fake.Count("commit"))
	}
	if inUse := pooled.Stats().InUse; inUse != 1 {
		t.Errorf("%d connections checked out during the transaction, want 1", inUse)
	}

	recorder, body := post(t, handler, "/CommitTransaction", models.CommitTransactionRequest{
		ResourceArn:   resourceArn,
		SecretArn:     secretArn,
		TransactionID: transactionID,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body %v", recorder.Code, body)
	}
	if body["transactionStatus"] != TransactionCommitted {
		t.Errorf("transactionStatus = %v, want %q", body["transactionStatus"], TransactionCommitted)
	}
	if fake.Count("commit") != 1 {
		t.Errorf("committed %d times, want 1", fake.Count("commit"))
	}
	assertNoLeakedConnections(t, pooled)

	// The transaction is gone once committed.
	recorder, _ = post(t, handler, "/CommitTransaction", models.CommitTransactionRequest{
		ResourceArn:   resourceArn,
		SecretArn:     secretArn,
		TransactionID: transactionID,
	})
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a spent transaction", recorder.Code)
	}
}

func TestRollbackTransaction(t *testing.T) {
	fake := testdb.New()
	handler, pooled := newTestServer(t, resource.MySQL, fake)

	_, begun := post(t, handler, "/BeginTransaction", models.BeginTransactionRequest{
		ResourceArn: resourceArn,
		SecretArn:   secretArn,
		Database:    "test",
	})
	transactionID := begun["transactionId"].(string)

	recorder, body := post(t, handler, "/RollbackTransaction", models.RollbackTransactionRequest{
		ResourceArn:   resourceArn,
		SecretArn:     secretArn,
		TransactionID: transactionID,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body %v", recorder.Code, body)
	}
	if body["transactionStatus"] != RollbackComplete {
		t.Errorf("transactionStatus = %v, want %q", body["transactionStatus"], RollbackComplete)
	}
	if fake.Count("rollback") != 1 {
		t.Errorf("rolled back %d times, want 1", fake.Count("rollback"))
	}
	assertNoLeakedConnections(t, pooled)
}

func TestCommitReportsAnInvalidTransactionID(t *testing.T) {
	handler, _ := newTestServer(t, resource.MySQL, testdb.New())

	recorder, body := post(t, handler, "/CommitTransaction", models.CommitTransactionRequest{
		ResourceArn:   resourceArn,
		SecretArn:     secretArn,
		TransactionID: "nope",
	})

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", recorder.Code)
	}
	if body["message"] != "Invalid transaction ID" {
		t.Errorf("message = %v", body["message"])
	}
}

func TestBatchExecute(t *testing.T) {
	fake := testdb.New()
	id := int64(0)
	fake.OnExec = func(string, []driver.Value) (*testdb.ExecResult, error) {
		id++
		return &testdb.ExecResult{RowsAffected: 1, LastInsertID: id}, nil
	}
	handler, _ := newTestServer(t, resource.MySQL, fake)

	name := func(v string) models.SqlParameter {
		return models.SqlParameter{Name: "name", Value: models.Field{StringValue: &v}}
	}

	recorder, body := post(t, handler, "/BatchExecute", models.BatchExecuteStatementRequest{
		ResourceArn: resourceArn,
		SecretArn:   secretArn,
		Database:    "test",
		Sql:         "INSERT INTO users (name) VALUES (:name)",
		ParameterSets: [][]models.SqlParameter{
			{name("ichiro")},
			{name("ken")},
		},
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body %v", recorder.Code, body)
	}
	results := body["updateResults"].([]any)
	if len(results) != 2 {
		t.Fatalf("updateResults = %v", results)
	}
	if fake.Count("commit") != 1 {
		t.Errorf("committed %d times, want 1", fake.Count("commit"))
	}
}

func TestBatchExecuteWithoutParameterSets(t *testing.T) {
	fake := testdb.New()
	handler, pooled := newTestServer(t, resource.MySQL, fake)

	recorder, body := post(t, handler, "/BatchExecute", models.BatchExecuteStatementRequest{
		ResourceArn: resourceArn,
		SecretArn:   secretArn,
		Database:    "test",
		Sql:         "INSERT INTO users (name) VALUES (:name)",
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body %v", recorder.Code, body)
	}
	results, ok := body["updateResults"].([]any)
	if !ok || len(results) != 0 {
		t.Errorf("updateResults = %v, want []", body["updateResults"])
	}
	assertNoLeakedConnections(t, pooled)
}

func TestGetIsNotAllowed(t *testing.T) {
	handler, _ := newTestServer(t, resource.MySQL, testdb.New())

	request := httptest.NewRequest(http.MethodGet, "/Execute", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", recorder.Code)
	}
}

func TestUnknownPathIsNotFound(t *testing.T) {
	handler, _ := newTestServer(t, resource.MySQL, testdb.New())

	request := httptest.NewRequest(http.MethodPost, "/Nope", strings.NewReader("{}"))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
}

// A transaction outlives the request that opened it. database/sql rolls a
// transaction back when the context it was begun with is cancelled, so
// beginning it under the request context would make every transaction useless
// the moment BeginTransaction returned.
func TestTransactionSurvivesTheRequestContext(t *testing.T) {
	fake := testdb.New()
	fake.OnExec = func(string, []driver.Value) (*testdb.ExecResult, error) {
		return &testdb.ExecResult{RowsAffected: 1}, nil
	}
	handler, _ := newTestServer(t, resource.MySQL, fake)

	// Serve BeginTransaction under a context that is cancelled as soon as the
	// request finishes, the way net/http does.
	body, err := json.Marshal(models.BeginTransactionRequest{
		ResourceArn: resourceArn,
		SecretArn:   secretArn,
		Database:    "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/BeginTransaction", strings.NewReader(string(body))).WithContext(ctx)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	cancel()

	var begun models.BeginTransactionResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &begun); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}

	// database/sql rolls back from a goroutine, so give it a moment to do the
	// wrong thing if the transaction is still tied to the cancelled context.
	time.Sleep(50 * time.Millisecond)
	if got := fake.Count("rollback"); got != 0 {
		t.Fatalf("transaction was rolled back %d times when the request ended", got)
	}

	recorder, responseBody := post(t, handler, "/Execute", models.ExecuteStatementRequest{
		ResourceArn:   resourceArn,
		SecretArn:     secretArn,
		Database:      "test",
		TransactionID: begun.TransactionID,
		Sql:           "INSERT INTO users (name) VALUES ('a')",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("statement in the transaction failed: %d %v", recorder.Code, responseBody)
	}
}
