// Package secrets holds the fake Secrets Manager the Data API authenticates
// against. It is a port of SecretManager.kt.
package secrets

import (
	"sync"

	"github.com/sonnh96/local-data-api/internal/apierr"
)

// Secret is the database credential a secret ARN resolves to.
type Secret struct {
	UserName string
	Password string
}

// Manager maps secret ARNs onto credentials.
type Manager struct {
	mu      sync.RWMutex
	secrets map[string]Secret
}

// NewManager returns an empty Manager.
func NewManager() *Manager {
	return &Manager{secrets: make(map[string]Secret)}
}

// SetSecret registers a credential under secretArn and returns the ARN.
func (m *Manager) SetSecret(userName, password, secretArn string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secrets[secretArn] = Secret{UserName: userName, Password: password}
	return secretArn
}

// GetSecret resolves a secret ARN, reporting the same BadRequestException the
// real service returns for an unknown ARN.
func (m *Manager) GetSecret(secretArn string) (Secret, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	secret, ok := m.secrets[secretArn]
	if !ok {
		return Secret{}, apierr.BadRequest(
			"Error fetching secret %s : Secrets Manager can’t find the specified secret. "+
				"(Service: AWSSecretsManager; Status Code: 400; Error Code: ResourceNotFoundException; "+
				"Request ID:  00000000-1111-2222-3333-44444444444)",
			secretArn,
		)
	}
	return secret, nil
}
