// Package server exposes the RDS Data API over HTTP. It is a port of the
// routing block in Application.kt.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/sonnh96/local-data-api/internal/apierr"
	"github.com/sonnh96/local-data-api/internal/models"
	"github.com/sonnh96/local-data-api/internal/resource"
	"github.com/sonnh96/local-data-api/internal/secrets"
)

// Transaction status strings reported by the commit and rollback endpoints.
const (
	TransactionCommitted = "Transaction Committed"
	RollbackComplete     = "Rollback Complete"
)

// Server routes Data API requests onto the resource manager.
type Server struct {
	resources *resource.Manager
	secrets   *secrets.Manager
	logger    *slog.Logger
}

// New returns a Server backed by the given managers.
func New(resources *resource.Manager, secretManager *secrets.Manager, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{resources: resources, secrets: secretManager, logger: logger}
}

// Handler builds the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /ExecuteSql", s.handle(s.executeSQL))
	mux.HandleFunc("POST /BeginTransaction", s.handle(s.beginTransaction))
	mux.HandleFunc("POST /CommitTransaction", s.handle(s.commitTransaction))
	mux.HandleFunc("POST /RollbackTransaction", s.handle(s.rollbackTransaction))
	mux.HandleFunc("POST /Execute", s.handle(s.executeStatement))
	mux.HandleFunc("POST /BatchExecute", s.handle(s.batchExecuteStatement))
	return mux
}

// handlerFunc is a route that returns the body to encode, or an error.
type handlerFunc func(*http.Request) (any, error)

// handle turns a handlerFunc into an http.HandlerFunc, rendering errors the way
// the StatusPages block does in Ktor.
func (s *Server) handle(next handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := next(r)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		s.writeJSON(w, http.StatusOK, body)
	}
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	apiErr := apierr.From(err)
	s.logger.Info("request failed",
		"path", r.URL.Path,
		"code", apiErr.Code,
		"status", apiErr.StatusCode,
		"message", apiErr.Message,
	)
	s.writeJSON(w, apiErr.StatusCode, models.ErrorResponse{
		Message: apiErr.Message,
		Code:    apiErr.Code,
	})
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		s.logger.Error("cannot write response", "error", err)
	}
}

// decode reads a JSON request body, rejecting anything that is not valid JSON.
// Unknown members are ignored, matching Ktor's ignoreUnknownKeys, so that a
// newer AWS SDK sending a field this build does not know about still works.
func decode[T any](r *http.Request) (T, error) {
	var body T
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		if errors.Is(err, io.EOF) {
			return body, apierr.BadRequest("Request body is empty")
		}
		return body, apierr.BadRequest("Cannot parse request body: %s", err.Error())
	}
	return body, nil
}

func (s *Server) executeSQL(r *http.Request) (any, error) {
	return nil, apierr.NotFound("NotImplemented")
}

func (s *Server) beginTransaction(r *http.Request) (any, error) {
	request, err := decode[models.BeginTransactionRequest](r)
	if err != nil {
		return nil, err
	}

	res, err := s.resource(r.Context(), request.ResourceArn, request.SecretArn, "", request.Database, request.Schema)
	if err != nil {
		return nil, err
	}

	return models.BeginTransactionResponse{TransactionID: res.Begin()}, nil
}

func (s *Server) commitTransaction(r *http.Request) (any, error) {
	request, err := decode[models.CommitTransactionRequest](r)
	if err != nil {
		return nil, err
	}

	res, err := s.resource(r.Context(), request.ResourceArn, request.SecretArn, request.TransactionID, "", "")
	if err != nil {
		return nil, err
	}
	defer res.Close()

	if err := res.Commit(); err != nil {
		return nil, err
	}
	return models.CommitTransactionResponse{TransactionStatus: TransactionCommitted}, nil
}

func (s *Server) rollbackTransaction(r *http.Request) (any, error) {
	request, err := decode[models.RollbackTransactionRequest](r)
	if err != nil {
		return nil, err
	}

	res, err := s.resource(r.Context(), request.ResourceArn, request.SecretArn, request.TransactionID, "", "")
	if err != nil {
		return nil, err
	}
	defer res.Close()

	if err := res.Rollback(); err != nil {
		return nil, err
	}
	return models.RollbackTransactionResponse{TransactionStatus: RollbackComplete}, nil
}

func (s *Server) executeStatement(r *http.Request) (any, error) {
	request, err := decode[models.ExecuteStatementRequest](r)
	if err != nil {
		return nil, err
	}

	res, err := s.resource(r.Context(), request.ResourceArn, request.SecretArn, request.TransactionID, request.Database, request.Schema)
	if err != nil {
		return nil, err
	}
	defer s.release(res)

	response, err := res.Execute(r.Context(), request.Sql, request.Parameters, request.IncludeResultMetadata)
	if err != nil {
		return nil, err
	}

	// A statement sent outside a transaction is its own transaction.
	if !res.InTransaction() {
		if err := res.Commit(); err != nil {
			return nil, err
		}
	}
	return response, nil
}

func (s *Server) batchExecuteStatement(r *http.Request) (any, error) {
	request, err := decode[models.BatchExecuteStatementRequest](r)
	if err != nil {
		return nil, err
	}

	res, err := s.resource(r.Context(), request.ResourceArn, request.SecretArn, request.TransactionID, request.Database, request.Schema)
	if err != nil {
		return nil, err
	}
	defer s.release(res)

	response, err := res.BatchExecute(r.Context(), request.Sql, request.ParameterSets)
	if err != nil {
		return nil, err
	}

	if !res.InTransaction() {
		if err := res.Commit(); err != nil {
			return nil, err
		}
	}
	return response, nil
}

// resource authenticates the secret ARN and resolves the resource.
func (s *Server) resource(ctx context.Context, resourceArn, secretArn, transactionID, database, schema string) (*resource.Resource, error) {
	secret, err := s.secrets.GetSecret(secretArn)
	if err != nil {
		return nil, err
	}
	return s.resources.GetResource(ctx, resourceArn, secret.UserName, secret.Password, transactionID, database, schema)
}

// release closes a resource that this request owns. A resource joined through a
// transaction ID stays open for the next request.
func (s *Server) release(res *resource.Resource) {
	if res.InTransaction() {
		return
	}
	// Roll back in case the handler returned before committing; on an already
	// committed transaction this is a no-op.
	res.Rollback()
	if err := res.Close(); err != nil {
		s.logger.Error("cannot close connection", "error", err)
	}
}
