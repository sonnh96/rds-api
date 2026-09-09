package resource

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/sonnh96/local-data-api/internal/apierr"
	"github.com/sonnh96/local-data-api/internal/testdb"
)

func newTestManager(t *testing.T, engine Engine) (*Manager, *testdb.DB) {
	t.Helper()

	fake := testdb.New()
	manager := NewManager()
	manager.SetOpener(fake.Opener())
	manager.SetResource(engine, "arn:resource", "127.0.0.1", 3306, nil)
	t.Cleanup(func() { manager.Close() })

	return manager, fake
}

func TestNewTransactionID(t *testing.T) {
	id := NewTransactionID()
	if len(id) != transactionIDLength {
		t.Errorf("length = %d, want %d", len(id), transactionIDLength)
	}
	for _, c := range id {
		if !strings.ContainsRune(transactionIDCharacters, c) {
			t.Fatalf("unexpected character %q in transaction ID", c)
		}
	}
	if NewTransactionID() == id {
		t.Error("two transaction IDs are identical")
	}
}

func TestGetResourceUnknownArn(t *testing.T) {
	manager, _ := newTestManager(t, MySQL)

	_, err := manager.GetResource(context.Background(), "arn:other", "root", "example", "", "", "")
	if err == nil {
		t.Fatal("expected an error for an unregistered resource ARN")
	}

	var apiErr *apierr.DataAPIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T, want *apierr.DataAPIError", err)
	}
	if apiErr.StatusCode != 400 {
		t.Errorf("status = %d, want 400", apiErr.StatusCode)
	}
	if want := "HttpEndPoint is not enabled for arn:other"; apiErr.Message != want {
		t.Errorf("message = %q, want %q", apiErr.Message, want)
	}
}

// A live transaction against an unregistered ARN is a server bug, not a client
// mistake, so it reports 500 rather than 400.
func TestGetResourceUnknownArnWithLiveTransaction(t *testing.T) {
	manager, _ := newTestManager(t, MySQL)
	ctx := context.Background()

	res, err := manager.GetResource(ctx, "arn:resource", "root", "example", "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	transactionID := res.Begin()

	_, err = manager.GetResource(ctx, "arn:other", "root", "example", transactionID, "", "")
	var apiErr *apierr.DataAPIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T, want *apierr.DataAPIError", err)
	}
	if apiErr.StatusCode != 500 {
		t.Errorf("status = %d, want 500", apiErr.StatusCode)
	}
}

func TestGetResourceInvalidTransactionID(t *testing.T) {
	manager, _ := newTestManager(t, MySQL)

	_, err := manager.GetResource(context.Background(), "arn:resource", "root", "example", "nope", "", "")
	var apiErr *apierr.DataAPIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T, want *apierr.DataAPIError", err)
	}
	if want := "Invalid transaction ID"; apiErr.Message != want {
		t.Errorf("message = %q, want %q", apiErr.Message, want)
	}
}

func TestGetResourceJoinsAnOpenTransaction(t *testing.T) {
	manager, fake := newTestManager(t, MySQL)
	ctx := context.Background()

	first, err := manager.GetResource(ctx, "arn:resource", "root", "example", "", "test", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	transactionID := first.Begin()

	second, err := manager.GetResource(ctx, "arn:resource", "root", "example", transactionID, "test", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if second.TransactionID != transactionID {
		t.Errorf("TransactionID = %q, want %q", second.TransactionID, transactionID)
	}
	if !second.InTransaction() {
		t.Error("resource should report that it is in a transaction")
	}

	// Joining a transaction must not open a second connection.
	if got := fake.Count("begin"); got != 1 {
		t.Errorf("began %d transactions, want 1", got)
	}
}

func TestGetResourceRejectsADifferentDatabase(t *testing.T) {
	manager, _ := newTestManager(t, MySQL)
	ctx := context.Background()

	first, err := manager.GetResource(ctx, "arn:resource", "root", "example", "", "test", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	transactionID := first.Begin()

	_, err = manager.GetResource(ctx, "arn:resource", "root", "example", transactionID, "other", "")
	var apiErr *apierr.DataAPIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T, want *apierr.DataAPIError", err)
	}
	if want := "Database name is not the same as when transaction was created"; apiErr.Message != want {
		t.Errorf("message = %q, want %q", apiErr.Message, want)
	}
}

func TestCloseForgetsTheTransaction(t *testing.T) {
	manager, _ := newTestManager(t, MySQL)
	ctx := context.Background()

	res, err := manager.GetResource(ctx, "arn:resource", "root", "example", "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	transactionID := res.Begin()
	if !manager.HasTransaction(transactionID) {
		t.Fatal("transaction should be registered")
	}

	if err := res.Commit(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := res.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if manager.HasTransaction(transactionID) {
		t.Error("transaction should be forgotten after Close")
	}
}

func TestPostgresSchemaIsAppliedToTheConnection(t *testing.T) {
	fake := testdb.New()
	manager := NewManager()
	manager.SetOpener(fake.Opener())
	manager.SetResource(Postgres, "arn:resource", "127.0.0.1", 5432, nil)
	t.Cleanup(func() { manager.Close() })

	_, err := manager.GetResource(context.Background(), "arn:resource", "postgres", "example", "", "test", "my schema")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := fake.Calls()
	if len(calls) == 0 {
		t.Fatal("expected the schema to be applied")
	}
	if want := `SET search_path TO "my schema"`; calls[0].Query != want {
		t.Errorf("query = %q, want %q", calls[0].Query, want)
	}
}

func TestMySQLSchemaIsNotApplied(t *testing.T) {
	manager, fake := newTestManager(t, MySQL)

	_, err := manager.GetResource(context.Background(), "arn:resource", "root", "example", "", "test", "ignored")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(fake.Calls()); got != 0 {
		t.Errorf("ran %d statements, want 0", got)
	}
}

func TestManagerReusesPools(t *testing.T) {
	opened := 0
	fake := testdb.New()
	manager := NewManager()
	manager.SetOpener(func(driverName, dsn string) (*sql.DB, error) {
		opened++
		return fake.Open(), nil
	})
	manager.SetResource(MySQL, "arn:resource", "127.0.0.1", 3306, nil)
	t.Cleanup(func() { manager.Close() })

	ctx := context.Background()
	for range 3 {
		res, err := manager.GetResource(ctx, "arn:resource", "root", "example", "", "test", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		res.Commit()
		res.Close()
	}

	if opened != 1 {
		t.Errorf("opened %d pools, want 1", opened)
	}
}
