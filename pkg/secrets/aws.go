package secrets

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

//go:generate moq -out mocks/secretsmanager.go -pkg mocks -skip-ensure -fmt goimports . secretsmanagerClient:SectretsManagerClient

// AWSSecretsProvider is a provider for AWS Secrets Manager
type AWSSecretsProvider struct {
	client secretsmanagerClient
}

type secretsmanagerClient interface {
	GetSecretValue(ctx context.Context, params *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

// NewAWSSecretsProvider creates a new instance of AWSSecretsProvider
func NewAWSSecretsProvider(accessKeyID, secretAccessKey, region string) (*AWSSecretsProvider, error) {
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, "")))

	if err != nil {
		return nil, fmt.Errorf("error creating aws config: %w", err)
	}
	return &AWSSecretsProvider{client: secretsmanager.NewFromConfig(cfg)}, nil
}

// Get gets a secret from AWS Secrets Manager
func (p *AWSSecretsProvider) Get(key string) (string, error) {
	input := &secretsmanager.GetSecretValueInput{SecretId: &key}
	result, err := p.client.GetSecretValue(context.Background(), input)
	if err != nil {
		return "", fmt.Errorf("error reading aws secret for %q: %w", key, err)
	}
	return *result.SecretString, nil
}
