// Package acme is a PLUGIN, and the point of it is what the host CANNOT do:
// name this package's config type. It is unexported, so no other package can
// declare a field of it, embed it, or unmarshal into it.
//
// That is the one case where "just pass the nested struct" does not work, and
// it is the case viper's Sub() and koanf's Cut() exist to serve. The answer
// here is different: the host hands over a SOURCE — where to read — and the
// plugin does its own Load.
package acme

import (
	"io"

	"github.com/ubgo/cfgkit"
)

// config is the plugin's own configuration, private to this package.
//
// It has its own defaults, its own key names, and its own secret marking. None
// of that is visible to, or the responsibility of, the host.
type config struct {
	Endpoint string `env:"ENDPOINT" default:"https://api.acme.test" doc:"where to send requests"`
	Retries  int    `env:"RETRIES" default:"3" doc:"attempts before giving up"`
	APIKey   string `env:"API_KEY" secret:"true" doc:"credential for the endpoint"`
}

// Validate is the plugin's own rule, enforced by the plugin's own Load. The
// host neither knows nor enforces it — which is the separation being
// demonstrated.
func (c *config) Validate() error {
	return cfgkit.Range("Retries", c.Retries, 0, 10)
}

// Plugin is the only thing the host sees.
type Plugin struct {
	cfg *config
	res *cfgkit.Result
}

// Configure takes SOURCES rather than values, so the host says where to read
// and never handles what was read.
//
// The second Load is legal, cheap and fully isolated because cfgkit has no
// global state — there is no shared registry for two Loads to collide in. That
// is the property this whole pattern rests on.
func (p *Plugin) Configure(sources ...any) error {
	cfg, res, err := cfgkit.Load[config](cfgkit.WithSources(sources...))
	if err != nil {
		return err
	}
	p.cfg, p.res = cfg, res
	return nil
}

// Endpoint exposes only what the host actually needs.
func (p *Plugin) Endpoint() string { return p.cfg.Endpoint }

// Retries exposes only what the host actually needs.
func (p *Plugin) Retries() int { return p.cfg.Retries }

// Explain writes the plugin's OWN provenance: three fields, not the host's as
// well. A Sub()-style API could not do this, because the sub-tree would still
// belong to the host's Result.
func (p *Plugin) Explain(w io.Writer) error { return p.res.Explain(w) }

// Document writes the plugin's OWN .env contract, so a deployment can be told
// which ACME_ keys exist without the host maintaining that list.
func Document(w io.Writer) error { return cfgkit.Document[config](w) }
