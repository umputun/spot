package secrets

import (
	"errors"
	"fmt"

	"github.com/hashicorp/vault/api"
)

// HashiVaultProvider is a provider for HashiCorp Vault
type HashiVaultProvider struct {
	client *api.Client
	path   string
}

// NewHashiVaultProvider creates a new HashiCorp Vault provider
func NewHashiVaultProvider(addr, path, token string) (*HashiVaultProvider, error) {
	config := &api.Config{
		Address: addr,
	}

	client, err := api.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("error creating vault client: %w", err)
	}

	client.SetToken(token)
	return &HashiVaultProvider{client: client, path: path}, nil
}

// Get gets a secret from HashiCorp Vault
func (p *HashiVaultProvider) Get(key string) (string, error) {
	secret, err := p.client.Logical().Read(p.path)
	if err != nil {
		return "", fmt.Errorf("error reading secret from vault: %w", err)
	}

	if secret == nil || secret.Data == nil {
		return "", errors.New("secret not found")
	}

	data, ok := secret.Data["data"].(map[string]any)
	if !ok {
		return "", errors.New("unexpected secret data format")
	}

	value, ok := data[key].(string)
	if !ok {
		return "", errors.New("unexpected secret value format")
	}
	return value, nil
}
