package secrets

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnsibleVaultProvider_Get(t *testing.T) {
	vaultPath := "testdata/test_ansible-vault"
	vaultSecret := "password"
	p, err := NewAnsibleVaultProvider(vaultPath, vaultSecret)
	require.NoError(t, err, "failed to create AnsibleVaultProvider")

	t.Run("secret found", func(t *testing.T) {
		encryptedSecret, err := p.Get("secret")
		require.NoError(t, nil, err, "Get method should not return an error")
		assert.Equal(t, "test-secret-data", encryptedSecret, "Get method should return the correct secret value")
	})

	t.Run("secret not found", func(t *testing.T) {
		_, err := p.Get("secret-2")
		require.EqualError(t, err, "not found key: secret-2")
	})
}

func TestAnsibleVaultProvider_Create(t *testing.T) {
	vaultPath := "testdata/test_ansible-vault"
	vaultPathInvalidYaml := "testdata/test_ansible-vault-invalid-yaml"
	wrongVaultFilePath := "testdata/wrong-test_ansible-vault"
	vaultFilePathIsNotRegularFile := "testdata/"
	vaultSecret := "password"
	wrongVaultSecret := "password0"

	t.Run("ansible vault not found", func(t *testing.T) {
		_, err := NewAnsibleVaultProvider(wrongVaultFilePath, vaultSecret)
		require.EqualError(t, err, "error get fileinfo of: testdata/wrong-test_ansible-vault")
	})

	t.Run("ansible vault is not a file", func(t *testing.T) {
		_, err := NewAnsibleVaultProvider(vaultFilePathIsNotRegularFile, vaultSecret)
		require.EqualError(t, err, "testdata/ is not a regular file")
	})

	t.Run("ansible vault wrong password", func(t *testing.T) {
		_, err := NewAnsibleVaultProvider(vaultPath, wrongVaultSecret)
		require.EqualError(t, err, "error decrypting file: testdata/test_ansible-vault")
	})

	t.Run("ansible vault error unmarshaling yaml", func(t *testing.T) {
		_, err := NewAnsibleVaultProvider(vaultPathInvalidYaml, vaultSecret)
		require.EqualError(t, err, "error during unmarshaling yaml file")
	})

}
