package apierr

import (
	"errors"
	"fmt"
	"testing"
)

func TestConstructors(t *testing.T) {
	tests := []struct {
		name       string
		err        *DataAPIError
		wantCode   string
		wantStatus int
	}{
		{"bad request", BadRequest("bad %s", "thing"), "BadRequestException", 400},
		{"forbidden", Forbidden("no"), "ForbiddenException", 403},
		{"not found", NotFound("gone"), "NotFoundException", 404},
		{"internal", InternalServerError(), "InternalServerErrorException", 500},
		{"unavailable", ServiceUnavailable("later"), "ServiceUnavailableError", 503},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err.Code != tc.wantCode {
				t.Errorf("Code = %q, want %q", tc.err.Code, tc.wantCode)
			}
			if tc.err.StatusCode != tc.wantStatus {
				t.Errorf("StatusCode = %d, want %d", tc.err.StatusCode, tc.wantStatus)
			}
			if tc.err.Error() != tc.err.Message {
				t.Errorf("Error() = %q, want %q", tc.err.Error(), tc.err.Message)
			}
		})
	}
}

func TestBadRequestFormats(t *testing.T) {
	if got, want := BadRequest("HttpEndPoint is not enabled for %s", "arn").Message, "HttpEndPoint is not enabled for arn"; got != want {
		t.Errorf("Message = %q, want %q", got, want)
	}
}

func TestInternalServerErrorMessage(t *testing.T) {
	if got, want := InternalServerError().Message, "InternalServerError"; got != want {
		t.Errorf("Message = %q, want %q", got, want)
	}
}

func TestFromPassesThroughDataAPIErrors(t *testing.T) {
	original := NotFound("NotImplemented")

	if got := From(original); got != original {
		t.Errorf("From returned %v, want the original error", got)
	}
}

func TestFromUnwrapsDataAPIErrors(t *testing.T) {
	original := BadRequest("inner")
	wrapped := fmt.Errorf("outer: %w", original)

	if got := From(wrapped); got != original {
		t.Errorf("From returned %v, want the wrapped error", got)
	}
}

func TestFromNil(t *testing.T) {
	if got := From(nil); got != nil {
		t.Errorf("From(nil) = %v, want nil", got)
	}
}

// codedError stands in for the driver adapter in the resource package.
type codedError struct {
	code    int
	message string
}

func (e codedError) Error() string { return e.message }

func (e codedError) DataAPIErrorCode() (int, string) { return e.code, e.message }

func TestFromDatabaseErrorReportsTheVendorCode(t *testing.T) {
	err := From(codedError{code: 1146, message: "Table 'test.nope' doesn't exist"})

	if err.StatusCode != 400 || err.Code != "BadRequestException" {
		t.Errorf("error = %+v", err)
	}
	want := "Database error code: 1146. Message: Table 'test.nope' doesn't exist"
	if err.Message != want {
		t.Errorf("Message = %q, want %q", err.Message, want)
	}
}

func TestFromPlainError(t *testing.T) {
	err := From(errors.New("something broke"))

	if err.StatusCode != 400 {
		t.Errorf("StatusCode = %d, want 400", err.StatusCode)
	}
	want := "Database error code: 0. Message: something broke"
	if err.Message != want {
		t.Errorf("Message = %q, want %q", err.Message, want)
	}
}
