package secrets

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestInternalProvider_EncryptionDecryption(t *testing.T) {
	p := &InternalProvider{
		key: []byte("test_key"),
	}

	er, err := p.encrypt("test_value")
	require.NoError(t, err)
	t.Logf("encrypted value: %s", er)
	dr, err := p.decrypt(er)
	require.NoError(t, err)
	assert.Equal(t, "test_value", dr)
}

func TestInternalProvider(t *testing.T) {
	ctx := context.Background()

	pgContainer, pgConnString, mysqlContainer, mysqlConnString := setupTestContainers(t)

	defer func() {
		require.NoError(t, pgContainer.Terminate(ctx))
		require.NoError(t, mysqlContainer.Terminate(ctx))
		require.NoError(t, os.Remove("/tmp/test_secrets.db"))
	}()

	testCases := []struct {
		name       string
		dbType     string
		connString string
		secretKey  string
	}{
		{
			name:       "SQLite",
			dbType:     "sqlite",
			connString: "file:///tmp/test_secrets.db",
			secretKey:  "test_key",
		},
		{
			name:       "PostgreSQL",
			dbType:     "postgres",
			connString: pgConnString,
			secretKey:  "test_key",
		},
		{
			name:       "MySQL",
			dbType:     "mysql",
			connString: mysqlConnString,
			secretKey:  "test_key",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewInternalProvider(tc.connString, []byte(tc.secretKey))
			require.NoError(t, err)

			err = provider.Set("test_key", "test_value")
			require.NoError(t, err)

			secret, err := provider.Get("test_key")
			require.NoError(t, err)
			assert.Equal(t, "test_value", secret)

			err = provider.Delete("test_key")
			require.NoError(t, err)

			_, err = provider.Get("test_key")
			require.Error(t, err)
		})
	}
}

func setupTestContainers(t *testing.T) (pc testcontainers.Container, ps string, mc testcontainers.Container, ms string) {
	t.Helper()
	ctx := context.Background()

	// pgSQL container
	pgReq := testcontainers.ContainerRequest{
		Image:        "postgres:15",
		ExposedPorts: []string{"5432/tcp"},
		Env:          map[string]string{"POSTGRES_PASSWORD": "password"},
		WaitingFor:   wait.ForLog("database system is ready to accept connections"),
	}
	pgContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: pgReq,
		Started:          true,
	})
	require.NoError(t, err)

	pgHost, err := pgContainer.Host(ctx)
	require.NoError(t, err)
	pgPort, err := pgContainer.MappedPort(ctx, "5432")
	require.NoError(t, err)
	pgConnString := fmt.Sprintf("postgres://postgres:password@%s:%d/postgres?sslmode=disable", pgHost, pgPort.Int())

	// MySQL container
	mysqlReq := testcontainers.ContainerRequest{
		Image:        "mysql:8",
		ExposedPorts: []string{"3306/tcp"},
		Env:          map[string]string{"MYSQL_ROOT_PASSWORD": "password"},
		WaitingFor:   wait.ForLog("port: 3306  MySQL Community Server - GPL"),
	}
	mysqlContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: mysqlReq,
		Started:          true,
	})
	require.NoError(t, err)
	mysqlHost, err := mysqlContainer.Host(ctx)
	require.NoError(t, err)
	mysqlPort, err := mysqlContainer.MappedPort(ctx, "3306")
	require.NoError(t, err)
	mysqlConnString := fmt.Sprintf("root:password@tcp(%s:%d)/mysql", mysqlHost, mysqlPort.Int())

	return pgContainer, pgConnString, mysqlContainer, mysqlConnString
}

func TestNoOp_Get(t *testing.T) {
	p := &NoOpProvider{}
	_, err := p.Get("test_key")
	require.Error(t, err)
}
