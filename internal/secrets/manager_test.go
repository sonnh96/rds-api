package secrets

import (
	"errors"
	"strings"
	"testing"

	"github.com/sonnh96/local-data-api/internal/apierr"
)

func TestSetAndGetSecret(t *testing.T) {
	manager := NewManager()

	arn := manager.SetSecret("root", "example", "arn:secret")
	if arn != "arn:secret" {
		t.Errorf("SetSecret returned %q, want %q", arn, "arn:secret")
	}

	secret, err := manager.GetSecret("arn:secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if secret.UserName != "root" || secret.Password != "example" {
		t.Errorf("GetSecret returned %+v", secret)
	}
}

func TestSetSecretOverwrites(t *testing.T) {
	manager := NewManager()
	manager.SetSecret("root", "example", "arn:secret")
	manager.SetSecret("other", "changed", "arn:secret")

	secret, err := manager.GetSecret("arn:secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if secret.UserName != "other" || secret.Password != "changed" {
		t.Errorf("GetSecret returned %+v", secret)
	}
}

func TestGetSecretUnknownArn(t *testing.T) {
	manager := NewManager()

	_, err := manager.GetSecret("arn:missing")
	if err == nil {
		t.Fatal("expected an error for an unknown secret ARN")
	}

	var apiErr *apierr.DataAPIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T, want *apierr.DataAPIError", err)
	}
	if apiErr.Code != "BadRequestException" || apiErr.StatusCode != 400 {
		t.Errorf("error = %+v", apiErr)
	}
	if !strings.Contains(apiErr.Message, "arn:missing") {
		t.Errorf("message does not name the ARN: %q", apiErr.Message)
	}
	if !strings.Contains(apiErr.Message, "ResourceNotFoundException") {
		t.Errorf("message is not the Secrets Manager one: %q", apiErr.Message)
	}
}

func TestManagerIsSafeForConcurrentUse(t *testing.T) {
	manager := NewManager()
	manager.SetSecret("root", "example", "arn:secret")

	done := make(chan struct{})
	for range 8 {
		go func() {
			defer func() { done <- struct{}{} }()
			for range 100 {
				manager.SetSecret("root", "example", "arn:secret")
				if _, err := manager.GetSecret("arn:secret"); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	for range 8 {
		<-done
	}
}
