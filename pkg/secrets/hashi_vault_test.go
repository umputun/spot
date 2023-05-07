package secrets

import (
	"context"
	"testing"

	"github.com/hashicorp/vault/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestHashiVaultProvider_Get(t *testing.T) {
	vaultC, vaultAddr := createVaultTestContainer(t)
	defer vaultC.Terminate(context.Background())

	// initialize vault client
	vaultClient, err := api.NewClient(&api.Config{Address: vaultAddr})
	require.NoError(t, err, "failed to create Vault client")
	vaultClient.SetToken("myroot-token")

	// write a secret to the Vault
	_, err = vaultClient.Logical().Write("secret/data/spot", map[string]interface{}{
		"data": map[string]string{"key1": "test-secret"},
	})
	require.NoError(t, err, "failed to write secret to Vault")

	// create HashiVaultProvider with the vault address and token
	hashiProvider, err := NewHashiVaultProvider(vaultAddr, "secret/data/spot", "myroot-token")
	require.NoError(t, err, "failed to create HashiVaultProvider")

	t.Run("existed key", func(t *testing.T) {
		secretValue, err := hashiProvider.Get("key1")
		require.NoError(t, err, "Get method should not return an error")
		assert.Equal(t, "test-secret", secretValue, "Get method should return the correct secret value")
	})

	t.Run("non-existed key", func(t *testing.T) {
		_, err := hashiProvider.Get("key2")
		require.Error(t, err, "Get method should return an error")
	})

	t.Run("invalid token", func(t *testing.T) {
		invalidProvider, err := NewHashiVaultProvider(vaultAddr, "secret/data/spot", "invalid-token")
		require.NoError(t, err, "failed to create HashiVaultProvider")

		_, err = invalidProvider.Get("key1")
		require.ErrorContains(t, err, "permission denied")
	})

	t.Run("invalid api address", func(t *testing.T) {
		invalidProvider, err := NewHashiVaultProvider("http://localhost:1234", "secret/data/spot", "myroot-token")
		require.NoError(t, err)
		_, err = invalidProvider.Get("key1")
		require.ErrorContains(t, err, "connection refused")
	})
}

func createVaultTestContainer(t *testing.T) (vaultC testcontainers.Container, vaultAddr string) {
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "vault:latest",
		ExposedPorts: []string{"8200/tcp"},
		Env: map[string]string{
			"VAULT_DEV_ROOT_TOKEN_ID":  "myroot-token",
			"VAULT_DEV_LISTEN_ADDRESS": "0.0.0.0:8200",
		},
		WaitingFor: wait.ForHTTP("/v1/sys/init").WithPort("8200/tcp"),
	}

	vaultC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start Vault container: %v", err)
	}

	host, _ := vaultC.Host(ctx)
	port, _ := vaultC.MappedPort(ctx, "8200")

	vaultAddr = "http://" + host + ":" + port.Port()
	return vaultC, vaultAddr
}
