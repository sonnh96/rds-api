// Package apierr contains the error types the Data API reports back to clients.
//
// It is a port of Exceptions.kt: every error carries an HTTP status code and a
// `code` string that the AWS SDKs use to build a typed exception client-side.
package apierr

import (
	"errors"
	"fmt"
	"net/http"
)

// DataAPIError is an error that maps onto a Data API error response.
type DataAPIError struct {
	// Code is the error name reported to the client, e.g. "BadRequestException".
	Code string
	// Message is the human readable description.
	Message string
	// StatusCode is the HTTP status the response is served with.
	StatusCode int
}

func (e *DataAPIError) Error() string { return e.Message }

// BadRequest builds a 400 BadRequestException.
func BadRequest(format string, args ...any) *DataAPIError {
	return &DataAPIError{
		Code:       "BadRequestException",
		Message:    fmt.Sprintf(format, args...),
		StatusCode: http.StatusBadRequest,
	}
}

// Forbidden builds a 403 ForbiddenException.
func Forbidden(format string, args ...any) *DataAPIError {
	return &DataAPIError{
		Code:       "ForbiddenException",
		Message:    fmt.Sprintf(format, args...),
		StatusCode: http.StatusForbidden,
	}
}

// NotFound builds a 404 NotFoundException.
func NotFound(format string, args ...any) *DataAPIError {
	return &DataAPIError{
		Code:       "NotFoundException",
		Message:    fmt.Sprintf(format, args...),
		StatusCode: http.StatusNotFound,
	}
}

// InternalServerError builds a 500 InternalServerErrorException.
func InternalServerError() *DataAPIError {
	return &DataAPIError{
		Code:       "InternalServerErrorException",
		Message:    "InternalServerError",
		StatusCode: http.StatusInternalServerError,
	}
}

// ServiceUnavailable builds a 503 ServiceUnavailableError.
func ServiceUnavailable(format string, args ...any) *DataAPIError {
	return &DataAPIError{
		Code:       "ServiceUnavailableError",
		Message:    fmt.Sprintf(format, args...),
		StatusCode: http.StatusServiceUnavailable,
	}
}

// From maps any error onto a DataAPIError. Errors that are already a
// *DataAPIError are returned untouched; a database error becomes a
// BadRequestException carrying the driver's own error code and message, which
// is how the real service reports a rejected statement.
func From(err error) *DataAPIError {
	if err == nil {
		return nil
	}
	var apiErr *DataAPIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	code, message := DriverError(err)
	return BadRequest("Database error code: %d. Message: %s", code, message)
}

// driverCoder is implemented by the per-driver adapters in the resource package
// so that apierr does not have to import the database drivers.
type driverCoder interface {
	DataAPIErrorCode() (int, string)
}

// DriverError extracts a numeric error code and message from a driver error.
func DriverError(err error) (int, string) {
	var coder driverCoder
	if errors.As(err, &coder) {
		return coder.DataAPIErrorCode()
	}
	return 0, err.Error()
}
