// Command plugin is a runnable answer to "how does a component configure
// itself when the host cannot name its config type?"
//
// Run it:
//
//	go run ./examples/plugin
//	PORT=9000 ACME_ENDPOINT=https://acme.prod ACME_API_KEY=sk_live go run ./examples/plugin
//
// The output is pinned by example_test.go in this directory, so it cannot rot.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/ubgo/cfgkit"
	"github.com/ubgo/cfgkit/examples/plugin/acme"
)

// hostConfig is the host's own configuration. Note what is NOT here: anything
// about acme. The host could not declare those fields even if it wanted to,
// because acme's config type is unexported in another package.
type hostConfig struct {
	Port int    `env:"PORT" default:"8080"`
	Name string `env:"APP_NAME" default:"demo"`
}

// plugin is the whole contract between host and plugin: be handed sources,
// configure yourself, and be able to explain yourself.
type plugin interface {
	Configure(sources ...any) error
	Explain(w io.Writer) error
}

func main() {
	if err := run(os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer) error {
	// 1. The host loads its own configuration.
	cfg, res, err := cfgkit.Load[hostConfig](cfgkit.WithSources(cfgkit.FromEnviron()))
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "host: %s listening on :%d\n\n", cfg.Name, cfg.Port)

	_, _ = fmt.Fprintln(w, "the HOST's Explain — only the host's fields:")
	if err := res.Explain(w); err != nil {
		return err
	}

	// 2. Each plugin configures itself under its own prefix. The host passes
	//    WHERE to read; it never sees what was read. Swapping this one line for
	//    FromFiles or a secret store changes where every plugin reads from,
	//    without any plugin knowing.
	client := &acme.Plugin{}
	plugins := map[string]plugin{"ACME_": client}
	for prefix, p := range plugins {
		if err := p.Configure(cfgkit.FromPrefixedEnviron(prefix)); err != nil {
			return fmt.Errorf("plugin %s: %w", prefix, err)
		}
	}

	// 3. The host USES the plugin, through the narrow surface the plugin chose
	//    to expose. This is the payoff of keeping the config type unexported:
	//    the host reads an endpoint and a retry count, and still cannot see —
	//    or accidentally log — the API key sitting beside them.
	_, _ = fmt.Fprintf(w, "\nthe HOST using the plugin: endpoint=%s retries=%d\n",
		client.Endpoint(), client.Retries())

	_, _ = fmt.Fprintln(w, "\nthe PLUGIN's Explain — only the plugin's fields, secret masked:")
	for _, p := range plugins {
		if err := p.Explain(w); err != nil {
			return err
		}
	}

	_, _ = fmt.Fprintln(w, "\nthe PLUGIN's own .env contract, which the host does not maintain:")
	return acme.Document(w)
}
