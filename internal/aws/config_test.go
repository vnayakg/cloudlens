package aws

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	awsV2 "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
)

// createDummyCredentialsFile creates a dummy AWS credentials file with the given content.
// It returns the path to the created file and a cleanup function.
func createDummyCredentialsFile(t *testing.T, content string) (string, func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "aws-creds-test")
	assert.NoError(t, err)

	credsFilePath := filepath.Join(tmpDir, "credentials")
	err = os.WriteFile(credsFilePath, []byte(content), 0600)
	assert.NoError(t, err)

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}
	return credsFilePath, cleanup
}

// createDummyConfigFile creates a dummy AWS config file with the given content.
// It returns the path to the created file and a cleanup function.
func createDummyConfigFile(t *testing.T, content string) (string, func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "aws-config-test")
	assert.NoError(t, err)

	configFilePath := filepath.Join(tmpDir, "config")
	err = os.WriteFile(configFilePath, []byte(content), 0600)
	assert.NoError(t, err)

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}
	return configFilePath, cleanup
}

func TestGetCfg_ProfilePrecedence(t *testing.T) {
	originalEnvVars := map[string]string{
		"AWS_ACCESS_KEY_ID":         os.Getenv("AWS_ACCESS_KEY_ID"),
		"AWS_SECRET_ACCESS_KEY":     os.Getenv("AWS_SECRET_ACCESS_KEY"),
		"AWS_SESSION_TOKEN":         os.Getenv("AWS_SESSION_TOKEN"),
		"AWS_PROFILE":               os.Getenv("AWS_PROFILE"),
		"AWS_CONFIG_FILE":           os.Getenv("AWS_CONFIG_FILE"),
		"AWS_SHARED_CREDENTIALS_FILE": os.Getenv("AWS_SHARED_CREDENTIALS_FILE"),
		"AWS_REGION":                os.Getenv("AWS_REGION"),
	}

	// Restore original env vars after test
	defer func() {
		for k, v := range originalEnvVars {
			if v == "" {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, v)
			}
		}
	}()

	t.Run("Uses Profile when cfgInput.Profile is set", func(t *testing.T) {
		dummyCredsContent := `
[test-profile]
aws_access_key_id = profile_access_key
aws_secret_access_key = profile_secret_key
`
		credsFile, credsCleanup := createDummyCredentialsFile(t, dummyCredsContent)
		defer credsCleanup()

		dummyConfigContent := `
[profile test-profile]
region = us-west-1
`
		configFile, configCleanup := createDummyConfigFile(t, dummyConfigContent)
		defer configCleanup()

		os.Setenv("AWS_SHARED_CREDENTIALS_FILE", credsFile)
		os.Setenv("AWS_CONFIG_FILE", configFile)
		os.Setenv("AWS_ACCESS_KEY_ID", "env_access_key") // Should be ignored
		os.Setenv("AWS_SECRET_ACCESS_KEY", "env_secret_key") // Should be ignored

		cfgInput := AWSConfigInput{Profile: "test-profile", Region: "us-east-1"} // Input region should take precedence

		awsCfg, err := GetCfg(cfgInput)
		assert.NoError(t, err)
		assert.NotNil(t, awsCfg)
		assert.Equal(t, "us-east-1", awsCfg.Region)

		creds, err := awsCfg.Credentials.Retrieve(context.TODO())
		assert.NoError(t, err)
		assert.Equal(t, "profile_access_key", creds.AccessKeyID)
		assert.Equal(t, "profile_secret_key", creds.SecretAccessKey)
	})

	t.Run("Uses Environment Variables when Profile is not set and ENV vars are present", func(t *testing.T) {
		// Unset profile-specific env vars to ensure they don't interfere
		os.Unsetenv("AWS_PROFILE")
		os.Unsetenv("AWS_CONFIG_FILE")
		os.Unsetenv("AWS_SHARED_CREDENTIALS_FILE")

		os.Setenv("AWS_ACCESS_KEY_ID", "env_access_key_id_val")
		os.Setenv("AWS_SECRET_ACCESS_KEY", "env_secret_access_key_val")
		os.Setenv("AWS_SESSION_TOKEN", "env_session_token_val")
		os.Setenv("AWS_REGION", "eu-central-1") // SDK should pick this up

		cfgInput := AWSConfigInput{} // No profile, no region

		awsCfg, err := GetCfg(cfgInput)
		assert.NoError(t, err)
		assert.NotNil(t, awsCfg)

		// Region might be picked up by SDK from AWS_REGION or other means if not in cfgInput
		// If AWS_REGION is set, SDK v2 tends to pick it.
		if region := os.Getenv("AWS_REGION"); region != "" {
			assert.Equal(t, region, awsCfg.Region)
		}


		creds, err := awsCfg.Credentials.Retrieve(context.TODO())
		assert.NoError(t, err)
		assert.Equal(t, "env_access_key_id_val", creds.AccessKeyID)
		assert.Equal(t, "env_secret_access_key_val", creds.SecretAccessKey)
		assert.Equal(t, "env_session_token_val", creds.SessionToken)
	})

	t.Run("Uses Default Profile when no Profile in input and no ENV vars for creds", func(t *testing.T) {
		dummyCredsContent := `
[default]
aws_access_key_id = default_profile_key
aws_secret_access_key = default_profile_secret
`
		credsFile, credsCleanup := createDummyCredentialsFile(t, dummyCredsContent)
		defer credsCleanup()

		dummyConfigContent := `
[default]
region = ap-southeast-2
`
		configFile, configCleanup := createDummyConfigFile(t, dummyConfigContent)
		defer configCleanup()

		os.Setenv("AWS_SHARED_CREDENTIALS_FILE", credsFile)
		os.Setenv("AWS_CONFIG_FILE", configFile)
		os.Unsetenv("AWS_ACCESS_KEY_ID")
		os.Unsetenv("AWS_SECRET_ACCESS_KEY")
		os.Unsetenv("AWS_SESSION_TOKEN")
		os.Unsetenv("AWS_PROFILE") // Ensure no profile env var is set

		cfgInput := AWSConfigInput{Region: "ca-central-1"} // Input region should take precedence

		awsCfg, err := GetCfg(cfgInput)
		assert.NoError(t, err)
		assert.NotNil(t, awsCfg)
		assert.Equal(t, "ca-central-1", awsCfg.Region)

		creds, err := awsCfg.Credentials.Retrieve(context.TODO())
		assert.NoError(t, err)
		assert.Equal(t, "default_profile_key", creds.AccessKeyID)
		assert.Equal(t, "default_profile_secret", creds.SecretAccessKey)
	})

	t.Run("Region from cfgInput takes precedence over profile region", func(t *testing.T) {
		dummyCredsContent := `[profile-region-test]
aws_access_key_id = key
aws_secret_access_key = secret`
		credsFile, credsCleanup := createDummyCredentialsFile(t, dummyCredsContent)
		defer credsCleanup()

		dummyConfigContent := `[profile profile-region-test]
region = us-west-2` // This region should be overridden
		configFile, configCleanup := createDummyConfigFile(t, dummyConfigContent)
		defer configCleanup()

		os.Setenv("AWS_SHARED_CREDENTIALS_FILE", credsFile)
		os.Setenv("AWS_CONFIG_FILE", configFile)
		os.Unsetenv("AWS_ACCESS_KEY_ID")
		os.Unsetenv("AWS_SECRET_ACCESS_KEY")

		cfgInput := AWSConfigInput{Profile: "profile-region-test", Region: "eu-north-1"}
		awsCfg, err := GetCfg(cfgInput)
		assert.NoError(t, err)
		assert.Equal(t, "eu-north-1", awsCfg.Region)
	})

	t.Run("Error on Invalid Profile and no other credentials", func(t *testing.T) {
		// Ensure no fallback credentials or valid files are present
		os.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/tmp/nonexistent-creds-file-for-error-test")
		os.Setenv("AWS_CONFIG_FILE", "/tmp/nonexistent-config-file-for-error-test")
		os.Unsetenv("AWS_ACCESS_KEY_ID")
		os.Unsetenv("AWS_SECRET_ACCESS_KEY")
		os.Unsetenv("AWS_PROFILE")

		cfgInput := AWSConfigInput{Profile: "invalid-profile-does-not-exist"}

		_, err := GetCfg(cfgInput)
		assert.Error(t, err)
		// Error message might vary depending on SDK version and what it tried first.
		// It should indicate that loading configuration or credentials failed.
		// Example: "failed to load configuration with profile invalid-profile-does-not-exist: ..."
		// Or "failed to load any AWS configuration: ..."
		t.Logf("Received error for invalid profile: %v", err)
		assert.True(t, strings.Contains(err.Error(), "failed to load any AWS configuration") || strings.Contains(err.Error(), "SharedConfigProfile"))
	})

	t.Run("Error on Missing Credentials entirely", func(t *testing.T) {
		os.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/tmp/empty-creds-"+strings.ToLower(t.Name()))
		os.Setenv("AWS_CONFIG_FILE", "/tmp/empty-config-"+strings.ToLower(t.Name()))
		// Ensure dummy files are empty or non-existent to simulate missing credentials
		emptyCredsFile, credsCleanup := createDummyCredentialsFile(t, "")
		defer credsCleanup()
		emptyConfigFile, configCleanup := createDummyConfigFile(t, "")
		defer configCleanup()

		os.Setenv("AWS_SHARED_CREDENTIALS_FILE", emptyCredsFile)
		os.Setenv("AWS_CONFIG_FILE", emptyConfigFile)
		os.Unsetenv("AWS_ACCESS_KEY_ID")
		os.Unsetenv("AWS_SECRET_ACCESS_KEY")
		os.Unsetenv("AWS_PROFILE")

		cfgInput := AWSConfigInput{}

		_, err := GetCfg(cfgInput)
		assert.Error(t, err)
		// This error usually indicates that the SDK couldn't find any credentials
		// in the default chain (env, shared config/credentials).
		t.Logf("Received error for missing credentials: %v", err)
		assert.True(t, strings.Contains(err.Error(), "failed to retrieve credentials") || strings.Contains(err.Error(), "no valid credentials"))
	})

	t.Run("SDK picks up region from AWS_REGION env var if not in cfgInput and not in profile", func(t *testing.T) {
		dummyCredsContent := `[default]
aws_access_key_id = default_key_for_region_test
aws_secret_access_key = default_secret_for_region_test`
		credsFile, credsCleanup := createDummyCredentialsFile(t, dummyCredsContent)
		defer credsCleanup()

		// Config file without a region for the default profile
		dummyConfigContent := `[default]
output = json`
		configFile, configCleanup := createDummyConfigFile(t, dummyConfigContent)
		defer configCleanup()

		os.Setenv("AWS_SHARED_CREDENTIALS_FILE", credsFile)
		os.Setenv("AWS_CONFIG_FILE", configFile)
		os.Setenv("AWS_REGION", "sa-east-1") // This should be picked up
		os.Unsetenv("AWS_ACCESS_KEY_ID")
		os.Unsetenv("AWS_SECRET_ACCESS_KEY")
		os.Unsetenv("AWS_PROFILE")

		cfgInput := AWSConfigInput{} // No profile, no region in input

		awsCfg, err := GetCfg(cfgInput)
		assert.NoError(t, err)
		assert.NotNil(t, awsCfg)
		assert.Equal(t, "sa-east-1", awsCfg.Region) // Assert SDK picked up AWS_REGION

		creds, err := awsCfg.Credentials.Retrieve(context.TODO())
		assert.NoError(t, err)
		assert.Equal(t, "default_key_for_region_test", creds.AccessKeyID)
	})
}

func TestGetCfg_LocalStack(t *testing.T) {
	originalPort := os.Getenv("LOCALSTACK_PORT")
	defer func() {
		if originalPort == "" {
			os.Unsetenv("LOCALSTACK_PORT")
		} else {
			os.Setenv("LOCALSTACK_PORT", originalPort)
		}
	}()

	os.Setenv("LOCALSTACK_PORT", "4566")

	cfgInput := AWSConfigInput{
		UseLocalStack: true,
		Region:        "us-east-1", // LocalStack still needs a nominal region
	}

	awsCfg, err := GetCfg(cfgInput)
	assert.NoError(t, err)
	assert.NotNil(t, awsCfg)
	assert.Equal(t, "us-east-1", awsCfg.Region)

	// For LocalStack, the endpoint resolver is the key.
	// We can't directly inspect the resolver function easily,
	// but we can check if it resolves to a LocalStack-like URL.
	endpoint, err := awsCfg.EndpointResolverWithOptions.ResolveEndpoint("s3", "us-east-1")
	assert.NoError(t, err)
	assert.Contains(t, endpoint.URL, "localhost:4566", "Endpoint URL should point to LocalStack")

	// Credentials for LocalStack are often dummy/test values or not strictly checked by LocalStack itself.
	// The default SDK behavior might still try to load some credentials,
	// but LocalStack typically doesn't validate them.
	// Depending on the SDK's default behavior when no explicit creds are found,
	// this might or might not return an error. For basic LocalStack usage,
	// the endpoint matters more than the specific credentials.
	_, credErr := awsCfg.Credentials.Retrieve(context.TODO())
	assert.NoError(t, credErr, "Retrieving credentials for LocalStack config should not fail, even if they are dummy/anonymous")
}

// Note: To make these tests fully hermetic and avoid REAL AWS calls if dummy files/env vars are misconfigured:
// - Consider setting os.Setenv("AWS_METADATA_SERVICE_TIMEOUT", "50ms") and
//   os.Setenv("AWS_EC2_METADATA_DISABLED", "true") to prevent IMDS lookups.
// - The current tests rely on the AWS SDK's default credential chain behavior when specific
//   env vars (AWS_CONFIG_FILE, AWS_SHARED_CREDENTIALS_FILE) are set to point to dummy files.
//   This is generally reliable for isolating from the user's actual ~/.aws directory.
// - Ensure that `GetCfg` doesn't have side effects like modifying global state that could
//   interfere between test runs (the current `GetCfg` looks safe in this regard).

// Helper to print credentials for debugging
func printCreds(t *testing.T, creds awsV2.Credentials, desc string) {
	t.Helper()
	retrieved, err := creds.Retrieve(context.TODO())
	if err != nil {
		t.Logf("[%s] Error retrieving creds: %v", desc, err)
		return
	}
	t.Logf("[%s] AccessKeyID: %s, SecretAccessKey: %s (len: %d), SessionToken: %s (len: %d)",
		desc, retrieved.AccessKeyID, retrieved.SecretAccessKey, len(retrieved.SecretAccessKey), retrieved.SessionToken, len(retrieved.SessionToken))
}

// Additional test cases to consider:
// - Profile specified in AWS_PROFILE environment variable.
// - Region specified in AWS_REGION environment variable and its precedence.
// - What happens if credentials file is present but config file is not, and vice-versa.
// - Tests for specific error types or messages if the SDK guarantees them (often not).
// - Behavior with AssumeRole (more complex, likely requires more sophisticated file setups).

func ExampleGetCfg_profile() {
	// Setup: Create temporary dummy AWS credentials and config files
	tmpDir, _ := os.MkdirTemp("", "example-profile")
	defer os.RemoveAll(tmpDir)

	credsFilePath := filepath.Join(tmpDir, "credentials")
	_ = os.WriteFile(credsFilePath, []byte("[myprofile]\naws_access_key_id=EXAMPLEKEY\naws_secret_access_key=EXAMPLESECRET"), 0600)

	configFilePath := filepath.Join(tmpDir, "config")
	_ = os.WriteFile(configFilePath, []byte("[profile myprofile]\nregion=us-west-2"), 0600)

	// Point SDK to these dummy files
	os.Setenv("AWS_SHARED_CREDENTIALS_FILE", credsFilePath)
	os.Setenv("AWS_CONFIG_FILE", configFilePath)
	// Unset others to ensure isolation for the example
	os.Unsetenv("AWS_ACCESS_KEY_ID")
	os.Unsetenv("AWS_SECRET_ACCESS_KEY")
	os.Unsetenv("AWS_PROFILE")


	cfgInput := AWSConfigInput{Profile: "myprofile"}
	cfg, err := GetCfg(cfgInput)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Region: %s\n", cfg.Region)
	creds, err := cfg.Credentials.Retrieve(context.TODO())
	if err != nil {
		fmt.Printf("Error retrieving credentials: %v\n", err)
		return
	}
	fmt.Printf("Access Key ID: %s\n", creds.AccessKeyID)
	// Output:
	// Region: us-west-2
	// Access Key ID: EXAMPLEKEY
}

func ExampleGetCfg_environmentVariables() {
	// Setup: Set environment variables for credentials
	os.Setenv("AWS_ACCESS_KEY_ID", "ENVKEY")
	os.Setenv("AWS_SECRET_ACCESS_KEY", "ENVSECRET")
	os.Setenv("AWS_REGION", "eu-central-1")
	// Unset file paths to ensure env vars are used
	os.Unsetenv("AWS_SHARED_CREDENTIALS_FILE")
	os.Unsetenv("AWS_CONFIG_FILE")
	os.Unsetenv("AWS_PROFILE")


	cfgInput := AWSConfigInput{} // No profile specified
	cfg, err := GetCfg(cfgInput)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Region: %s\n", cfg.Region)
	creds, err := cfg.Credentials.Retrieve(context.TODO())
	if err != nil {
		fmt.Printf("Error retrieving credentials: %v\n", err)
		return
	}
	fmt.Printf("Access Key ID: %s\n", creds.AccessKeyID)

	// Cleanup: Unset environment variables
	os.Unsetenv("AWS_ACCESS_KEY_ID")
	os.Unsetenv("AWS_SECRET_ACCESS_KEY")
	os.Unsetenv("AWS_REGION")
	// Output:
	// Region: eu-central-1
	// Access Key ID: ENVKEY
}
