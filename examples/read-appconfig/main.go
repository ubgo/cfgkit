// Command read-appconfig reads a deployed profile from AWS AppConfig.
//
// AppConfig is a TWO-CALL protocol: start a session, then read from it. The
// adapter makes both and names which one failed, because "start" and "read"
// fail for different reasons — permissions on the profile versus nothing
// deployed to it.
//
// It RUNS with no AWS account, through the module's API seam.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	ac "github.com/aws/aws-sdk-go-v2/service/appconfigdata"
	"github.com/ubgo/cfgkit"
	acsrc "github.com/ubgo/cfgkit/contrib/source-appconfig"
)

// Config nests, because a profile holds a document.
type Config struct {
	Service string `json:"service" env:"SERVICE" default:"api"`

	Server struct {
		Port int    `json:"port" env:"PORT" default:"8080"`
		Host string `json:"host" env:"HOST" default:"0.0.0.0"`
	} `json:"server"`
}

const (
	application = "checkout"
	environment = "production"
	profile     = "config"
)

// fakeAppConfig implements acsrc.API and records the call order, so the
// example can show that both calls happen and in which sequence.
type fakeAppConfig struct {
	document []byte
	calls    []string
}

// StartConfigurationSession is the first of AppConfig's two calls. It records
// the call so the example can show that both happen, and in which order.
func (f *fakeAppConfig) StartConfigurationSession(_ context.Context,
	in *ac.StartConfigurationSessionInput,
	_ ...func(*ac.Options)) (*ac.StartConfigurationSessionOutput, error) {
	f.calls = append(f.calls, "StartConfigurationSession")
	token := "session-token"
	return &ac.StartConfigurationSessionOutput{InitialConfigurationToken: &token}, nil
}

// GetLatestConfiguration is the second call, which needs the token the first
// returned. It records the call for the same reason.
func (f *fakeAppConfig) GetLatestConfiguration(_ context.Context,
	in *ac.GetLatestConfigurationInput,
	_ ...func(*ac.Options)) (*ac.GetLatestConfigurationOutput, error) {
	f.calls = append(f.calls, "GetLatestConfiguration")
	next := "next-token"
	return &ac.GetLatestConfigurationOutput{
		Configuration:              f.document,
		NextPollConfigurationToken: &next,
	}, nil
}

func main() {
	if err := run(os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer) error {
	client := &fakeAppConfig{
		document: []byte(`{"service":"checkout","server":{"port":9100}}`),
	}

	// Real usage: acsrc.JSON("checkout", "production", "config").
	cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
		acsrc.JSON(application, environment, profile, acsrc.WithClient(client)),
		cfgkit.FromEnviron(),
	))
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(w, "%s on %s:%d\n", cfg.Service, cfg.Server.Host, cfg.Server.Port)
	_, _ = fmt.Fprintf(w, "calls: %v\n", client.calls)
	_, _ = fmt.Fprintln(w)

	// No session is kept. Polling for changes is Watcher's job, not a source's
	// — a source that held a session open would be doing lifecycle management
	// the caller never asked for.
	return res.Explain(w)
}
