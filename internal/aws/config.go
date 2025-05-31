package aws

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	awsV2 "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	awsV2Config "github.com/aws/aws-sdk-go-v2/config"
	creds "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/defaults"
	"github.com/one2nc/cloudlens/internal"
	"github.com/rs/zerolog/log"
	"gopkg.in/ini.v1"
)

type Profiles struct {
	Data  []string
	Error string
}
type credentialProvider struct {
	awsV2.Credentials
}

type AWSConfigInput struct {
	Profile, Region string
	UseLocalStack   bool
}

func (c credentialProvider) Retrieve() (credentials.Value, error) {
	return credentials.Value{AccessKeyID: c.AccessKeyID, SecretAccessKey: c.SecretAccessKey, SessionToken: os.Getenv("AWS_SESSION_TOKEN")}, nil
}

func (c credentialProvider) IsExpired() bool {
	return c.Expired()
}

func GetCfg(cfgInput AWSConfigInput) (awsV2.Config, error) {
	var cfg awsV2.Config
	var err error
	var loadOptions []func(*awsV2Config.LoadOptions) error

	// Handle LocalStack separately if needed.
	// For this refactoring, we assume UseLocalStack might be handled upstream
	// or integrated differently. If it's still a primary concern for this function,
	// it would need to be the first check.
	if cfgInput.UseLocalStack {
		cfg, err = GetLocalstackCfg(cfgInput.Region)
		if err != nil {
			log.Print("failed to load LocalStack config: ", err)
			return awsV2.Config{}, fmt.Errorf("failed to load LocalStack config: %w", err)
		}
		// Early return for LocalStack as its configuration is distinct.
		return cfg, nil
	}

	// Region configuration
	if cfgInput.Region != "" {
		loadOptions = append(loadOptions, awsV2Config.WithRegion(cfgInput.Region))
	}

	// 1. Use profile from cfgInput.Profile if provided
	if cfgInput.Profile != "" {
		log.Print(fmt.Sprintf("Attempting to load configuration with profile: %s", cfgInput.Profile))
		profileLoadOptions := append(loadOptions, awsV2Config.WithSharedConfigProfile(cfgInput.Profile))
		cfg, err = awsV2Config.LoadDefaultConfig(context.TODO(), profileLoadOptions...)
		if err == nil {
			_, credErr := cfg.Credentials.Retrieve(context.TODO())
			if credErr == nil {
				log.Print(fmt.Sprintf("Successfully loaded configuration with profile: %s", cfgInput.Profile))
				return cfg, nil
			}
			log.Print(fmt.Sprintf("Failed to retrieve credentials with profile %s: %v", cfgInput.Profile, credErr))
			// Fall through if profile credentials fail, to allow other methods
		} else {
			log.Print(fmt.Sprintf("Failed to load configuration with profile %s: %v", cfgInput.Profile, err))
			// Fall through to try other methods
		}
	}

	// 2. Use AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY environment variables if set
	awsAccessKeyID := os.Getenv("AWS_ACCESS_KEY_ID")
	awsSecretAccessKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
	awsSessionToken := os.Getenv("AWS_SESSION_TOKEN") // Optional

	if awsAccessKeyID != "" && awsSecretAccessKey != "" {
		log.Print("Attempting to load configuration using environment variables (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY)")
		envLoadOptions := append(loadOptions, awsV2Config.WithCredentialsProvider(
			creds.NewStaticCredentialsProvider(awsAccessKeyID, awsSecretAccessKey, awsSessionToken),
		))
		cfg, err = awsV2Config.LoadDefaultConfig(context.TODO(), envLoadOptions...)
		if err == nil {
			_, credErr := cfg.Credentials.Retrieve(context.TODO())
			if credErr == nil {
				log.Print("Successfully loaded configuration using environment variables.")
				return cfg, nil
			}
			log.Print(fmt.Sprintf("Failed to retrieve credentials with environment variables: %v", credErr))
			// Fall through if env var credentials fail
		} else {
			log.Print(fmt.Sprintf("Failed to load configuration with environment variables: %v", err))
			// Fall through to try other methods
		}
	}

	// 3. Otherwise, use the default AWS profile/chain
	log.Print("Attempting to load configuration using default AWS profile/chain.")
	cfg, err = awsV2Config.LoadDefaultConfig(context.TODO(), loadOptions...)
	if err != nil {
		log.Print("Failed to load default AWS configuration: ", err)
		return awsV2.Config{}, fmt.Errorf("failed to load any AWS configuration: %w", err)
	}

	// Validate credentials once a configuration is potentially loaded
	finalCreds, err := cfg.Credentials.Retrieve(context.TODO())
	if err != nil {
		log.Print("Failed to retrieve credentials from the loaded AWS config: ", err)
		return awsV2.Config{}, fmt.Errorf("failed to retrieve credentials from loaded config: %w", err)
	}

	credentialProvider := credentialProvider{Credentials: finalCreds}
	if credentialProvider.IsExpired() {
		log.Print("AWS Credentials have expired.")
		return awsV2.Config{}, errors.New("AWS credentials expired")
	}

	log.Print("Successfully loaded AWS configuration.")
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
			URL:           GetLocastackEndpoint(),
			SigningRegion: region,
		}, nil
	})

	awsLSCfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion(region),
		config.WithEndpointResolver(customResolver),
	)
	if err != nil {
		log.Fatal().Err(err)
	}
	return awsLSCfg, nil
}

func GetLocastackEndpoint() string {

	port := os.Getenv(internal.LOCALSTACK_PORT)

	return fmt.Sprintf("http://localhost:%v", port)
}
