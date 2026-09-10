// Package appconfig reads an AWS AppConfig configuration as a cfgkit source.
//
// LIKE THE OTHER AWS ADAPTERS it takes a real dependency, for the reason the
// catalogue gives: credential resolution is the part worth delegating.
//
// APPCONFIG IS A TWO-CALL PROTOCOL, and that is the thing to know before using
// it. StartConfigurationSession returns a token; GetLatestConfiguration
// exchanges the token for the configuration AND a NEXT token. The first
// response after a session starts always carries the configuration; a later
// poll with an unchanged configuration returns EMPTY content, meaning "nothing
// new", not "no configuration".
//
// cfgkit reads once at boot, so this package makes exactly one of each call and
// keeps neither the session nor the token. Polling for changes is a different
// job: pair cfgkit.Watcher with your own trigger, and each Reload starts a
// fresh session. Holding a session open across reloads would make the emptiness
// rule matter, and getting it wrong means a service that silently keeps its
// first configuration forever.
package appconfig

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	ac "github.com/aws/aws-sdk-go-v2/service/appconfigdata"
	"github.com/ubgo/cfgkit"
)

// API is the slice of the AppConfig Data client this package uses.
//
// An interface rather than the concrete client, so a test implements it in
// twenty lines and this module's suite needs no AWS, no credentials and no
// network.
type API interface {
	StartConfigurationSession(ctx context.Context, in *ac.StartConfigurationSessionInput,
		optFns ...func(*ac.Options)) (*ac.StartConfigurationSessionOutput, error)
	GetLatestConfiguration(ctx context.Context, in *ac.GetLatestConfigurationInput,
		optFns ...func(*ac.Options)) (*ac.GetLatestConfigurationOutput, error)
}

type options struct {
	client API
	ctx    context.Context
}

// Option configures a source.
type Option func(*options)

// WithClient supplies the AppConfig Data client. The default builds one from
// the SDK's standard credential chain.
func WithClient(c API) Option { return func(o *options) { o.client = c } }

// WithContext bounds both calls, so a slow API cannot hold up a boot beyond
// what the caller allows.
func WithContext(ctx context.Context) Option { return func(o *options) { o.ctx = ctx } }

// Decoder turns the configuration's bytes into fields on the destination
// struct.
//
// AppConfig stores freeform documents — JSON, YAML, or text — and reports the
// content type but does not parse. So the reading is the caller's, the same way
// it is for source-s3, and this module needs no format dependency.
type Decoder func(doc []byte, dst any) error

// Configuration returns a structured source that reads one AppConfig
// configuration profile and decodes it with decode.
//
//	appconfig.Configuration("my-app", "prod", "app-config", json.Unmarshal)
//
// The three names are AppConfig's own: application, environment, profile.
func Configuration(application, environment, profile string, decode Decoder,
	opts ...Option) cfgkit.StructuredSource {
	o := &options{}
	for _, fn := range opts {
		fn(o)
	}

	name := fmt.Sprintf("appconfig:%s/%s/%s", application, environment, profile)
	doc, err := fetch(application, environment, profile, o)

	return cfgkit.StructuredFunc(name, func(dst any) error {
		if err != nil {
			return err
		}
		if decode == nil {
			return fmt.Errorf("%s: no decoder given", name)
		}
		if err := decode(doc, dst); err != nil {
			return fmt.Errorf("decoding %s: %w", name, err)
		}
		return nil
	})
}

// JSON is Configuration with encoding/json, which is the one format this
// module can offer without taking a dependency for it.
func JSON(application, environment, profile string, opts ...Option) cfgkit.StructuredSource {
	return Configuration(application, environment, profile, json.Unmarshal, opts...)
}

func fetch(application, environment, profile string, o *options) ([]byte, error) {
	ctx := o.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	client := o.client
	if client == nil {
		cfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("loading AWS configuration: %w", err)
		}
		client = ac.NewFromConfig(cfg)
	}

	where := fmt.Sprintf("%s/%s/%s", application, environment, profile)

	session, err := client.StartConfigurationSession(ctx, &ac.StartConfigurationSessionInput{
		ApplicationIdentifier:          aws.String(application),
		EnvironmentIdentifier:          aws.String(environment),
		ConfigurationProfileIdentifier: aws.String(profile),
	})
	if err != nil {
		return nil, fmt.Errorf("starting AppConfig session for %s: %w", where, err)
	}

	out, err := client.GetLatestConfiguration(ctx, &ac.GetLatestConfigurationInput{
		ConfigurationToken: session.InitialConfigurationToken,
	})
	if err != nil {
		return nil, fmt.Errorf("reading AppConfig %s: %w", where, err)
	}

	// THE FIRST call of a session always carries the configuration. Empty
	// content here therefore means the profile genuinely has none — not
	// "unchanged", which is what empty means on a LATER poll. Treating it as
	// unchanged would bind an empty document and look like a working read.
	if len(out.Configuration) == 0 {
		return nil, fmt.Errorf("AppConfig %s returned no configuration "+
			"(the profile exists but has no deployed content)", where)
	}
	return out.Configuration, nil
}
