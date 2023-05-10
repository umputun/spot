package secrets

import "errors"

// MemoryProvider is a secret provider that stores secrets in memory.
// Not recommended for production use, made for testing purposes.
type MemoryProvider struct {
	secrets map[string]string
}

// NewMemoryProvider creates a new MemoryProvider with the given secrets.
func NewMemoryProvider(secrets map[string]string) *MemoryProvider {
	return &MemoryProvider{secrets: secrets}
}

// Get returns the secret for the given key.
func (m *MemoryProvider) Get(key string) (string, error) {
	if val, ok := m.secrets[key]; ok {
		return val, nil
	}
	return "", errors.New("secret not found")
}
