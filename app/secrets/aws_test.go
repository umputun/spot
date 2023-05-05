package secrets

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/spot/app/secrets/mocks"
)

func TestAWSSecretsProvider_Get(t *testing.T) {
	a, err := NewAWSSecretsProvider("key", "secret", "region")
	require.NoError(t, err, "failed to create AWSSecretsProvider")

	sm := &mocks.SectretsManagerClient{
		GetSecretValueFunc: func(ctx context.Context, params *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
			if *params.SecretId == "key1" {
				res := "test-secret"
				return &secretsmanager.GetSecretValueOutput{SecretString: &res}, nil
			}
			return nil, errors.New("error 123")
		},
	}

	a.client = sm

	t.Run("secret found", func(t *testing.T) {
		secretValue, err := a.Get("key1")
		require.NoError(t, err, "Get method should not return an error")
		assert.Equal(t, "test-secret", secretValue, "Get method should return the correct secret value")
	})

	t.Run("secret not found", func(t *testing.T) {
		_, err := a.Get("key2")
		require.EqualError(t, err, "error reading aws secret for \"key2\": error 123")
	})
}
