package secrets

import (
	"fmt"
	"log"
	"os"

	vault "github.com/sosedoff/ansible-vault-go"
	yaml "gopkg.in/yaml.v3"
)

// AnsibleVaultProvider is a provider for ansible-vault files
type AnsibleVaultProvider struct {
	data map[string]interface{}
}

// NewAnsibleVaultProvider creates a new instance of AnsibleVaultProvider
func NewAnsibleVaultProvider(vaultPath, secret string) (*AnsibleVaultProvider, error) {
	fi, err := os.Lstat(vaultPath)
	if err != nil {
		return nil, fmt.Errorf("error get fileinfo of: %s", vaultPath)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", vaultPath)
	}

	// Decrypt ansible-vault
	decryptedVault, err := vault.DecryptFile(vaultPath, secret)
	if err != nil {
		return nil, fmt.Errorf("error decrypting file: %s", vaultPath)
	}
	log.Printf("[INFO] ansible vault file decrypted")

	// Unmarshal decrypted data
	m := make(map[string]interface{})
	err = yaml.Unmarshal([]byte(decryptedVault), &m)
	if err != nil {
		return nil, fmt.Errorf("error during unmarshaling yaml file")
	}
	return &AnsibleVaultProvider{m}, nil
}

// Get decrypted data from ansible-vault file
func (p *AnsibleVaultProvider) Get(key string) (string, error) {
	if keyValue, ok := p.data[key]; ok {
		return fmt.Sprintf("%v", keyValue), nil
	}
	return "", fmt.Errorf("not found key: %v", key)
}
