package aws

import (
	"context"
	// "errors" // No longer needed after simplifying credential error handling
	"fmt"
	"os"
	"strings"

	awsV2 "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	awsV2Config "github.com/aws/aws-sdk-go-v2/config"
	creds "github.com/aws/aws-sdk-go-v2/credentials"
	// "github.com/aws/aws-sdk-go/aws/credentials" // Unused AWS SDK v1 import, removed.
	"github.com/aws/aws-sdk-go/aws/defaults"
	"github.com/one2nc/cloudlens/internal"
	"github.com/rs/zerolog/log"
	"gopkg.in/ini.v1"
)

type Profiles struct {
	Data  []string
	Error string
}

type AWSConfigInput struct {
	Profile, Region string
	UseLocalStack   bool
}

// The credentialProvider struct and its methods Retrieve() and IsExpired()
// were designed for AWS SDK v1 style credentials.
// AWS SDK v2's `config.LoadDefaultConfig` and the `aws.CredentialsProvider`
// interface handle credential loading and caching, including refreshing expired
// credentials where possible (e.g., with IAM roles).
// Explicit expiry checks like the one previously here are often not needed
// with SDK v2, as errors during credential retrieval (e.g., when making a service call)
// will indicate issues.
// Thus, the credentialProvider struct and its methods are removed.

func GetCfg(cfgInput AWSConfigInput) (awsV2.Config, error) {
	if cfgInput.UseLocalStack {
		log.Debug().Msg("Attempting to load LocalStack configuration")
		cfg, err := GetLocalstackCfg(cfgInput.Region)
		if err != nil {
			log.Error().Err(err).Msg("Failed to load LocalStack config")
			return awsV2.Config{}, fmt.Errorf("failed to load LocalStack config: %w", err)
		}
		log.Debug().Msg("Successfully loaded LocalStack configuration")
		return cfg, nil
	}

	var loadOptions []func(*awsV2Config.LoadOptions) error

	// If a region is specified in the input, it takes precedence.
	if cfgInput.Region != "" {
		log.Debug().Msgf("Using region: %s from cfgInput", cfgInput.Region)
		loadOptions = append(loadOptions, awsV2Config.WithRegion(cfgInput.Region))
	}

	// If a profile is specified in the input, it's used for loading.
	// Otherwise, the SDK's default credential chain is used (env vars, default profile, etc.).
	if cfgInput.Profile != "" {
		log.Debug().Msgf("Using profile: %s from cfgInput", cfgInput.Profile)
		loadOptions = append(loadOptions, awsV2Config.WithSharedConfigProfile(cfgInput.Profile))
	}

	log.Debug().Msg("Attempting to load AWS configuration using awsV2Config.LoadDefaultConfig")
	cfg, err := awsV2Config.LoadDefaultConfig(context.TODO(), loadOptions...)
	if err != nil {
		log.Error().Err(err).Msg("Failed to load AWS configuration")
		return awsV2.Config{}, fmt.Errorf("failed to load AWS configuration: %w", err)
	}

	// Validate credentials by attempting to retrieve them.
	// The SDK's Retrieve method will return an error if credentials are not valid (e.g. expired or not found).
	_, err = cfg.Credentials.Retrieve(context.TODO())
	if err != nil {
		log.Error().Err(err).Msg("Failed to retrieve/validate credentials from the loaded AWS config")
		// The AWS SDK v2 will return an error if no credentials can be resolved (e.g., not found, expired and unrefreshable).
		// We return this error wrapped, allowing callers to inspect it if necessary.
		// Specific error messages about how to configure credentials can be handled by the calling UI/application layer
		// based on the context or the nature of the error.
		return awsV2.Config{}, fmt.Errorf("failed to retrieve or validate AWS credentials: %w", err)
	}

	log.Debug().Msg("Successfully loaded AWS configuration and validated credentials")
	return cfg, nil
}

func GetProfiles() (profiles []string, err error) {
	fpCred := defaults.SharedCredentialsFilename()
	_, errCred := os.Stat(fpCred)
	fpConf := defaults.SharedConfigFilename()
	_, errConf := os.Stat(fpConf)
	if os.IsNotExist(errCred) && os.IsNotExist(errConf) {
		return nil, errConf
	}
	var ret []string
	defaultReturn := &Profiles{Data: nil, Error: ""}
	fp := defaults.SharedCredentialsFilename()
	_, err = os.Stat(fp)
	if os.IsNotExist(err) {
		fp = defaults.SharedConfigFilename()
	}
	f, err := ini.Load(fp) // Load ini file
	if err != nil {
		defaultReturn.Error = err.Error()
	} else {
		arr := []string{}
		for _, v := range f.Sections() {
			if len(v.Keys()) != 0 {
				arr = append(arr, v.Name())
			}
		}
		defaultReturn.Data = arr
	}
	for i := 0; i < len(defaultReturn.Data); i++ {
		spltiArr := strings.Split(defaultReturn.Data[i], " ")
		if len(spltiArr) == 1 {
			ret = append(ret, spltiArr[len(spltiArr)-1])
		} else if len(spltiArr) > 1 && spltiArr[0] == "profile" {
			ret = append(ret, spltiArr[len(spltiArr)-1])
		}
	}
	return ret, nil
}

func GetLocalstackCfg(region string) (awsV2.Config, error) {
	customResolver := awsV2.EndpointResolverFunc(func(service, region string) (awsV2.Endpoint, error) {
		return awsV2.Endpoint{
			URL:           GetLocastackEndpoint(), // This already logs and defaults the port
			SigningRegion: region,               // Use the provided region for signing
		}, nil
	})

	effectiveRegion := region
	if effectiveRegion == "" {
		effectiveRegion = "us-east-1" // Default region for LocalStack if none provided
		log.Debug().Msgf("No region provided for LocalStack, defaulting to %s", effectiveRegion)
	}

	log.Debug().Msgf("Attempting to load LocalStack configuration for region: %s", effectiveRegion)
	awsLSCfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion(effectiveRegion),
		config.WithEndpointResolver(customResolver),
		// For LocalStack, explicitly use static/anonymous credentials as it often doesn't require/use real AWS creds.
		config.WithCredentialsProvider(creds.NewStaticCredentialsProvider("local", "local", "local")),
	)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to load LocalStack configuration for region %s", effectiveRegion)
		return awsV2.Config{}, fmt.Errorf("failed to load LocalStack configuration for region %s: %w", effectiveRegion, err)
	}
	log.Debug().Msgf("Successfully loaded LocalStack configuration for region %s", effectiveRegion)
	return awsLSCfg, nil
}

func GetLocastackEndpoint() string {
	port := os.Getenv(internal.LOCALSTACK_PORT)
	if port == "" {
		port = "4566" // Default LocalStack port
		log.Debug().Msgf("LOCALSTACK_PORT environment variable not set, defaulting to port %s", port)
	}
	endpoint := fmt.Sprintf("http://localhost:%s", port)
	log.Debug().Msgf("Using LocalStack endpoint: %s", endpoint)
	return endpoint
}
