// Package mock holds ONE canonical configuration, expressed in every format
// cfgkit can read, together with the struct that binds it and the values it
// must produce.
//
// Why it exists: each format module has its own tests, and each proves that
// module parses its own format. None of them can prove the thing a user
// actually depends on — that YAML, TOML, HCL, INI, .properties, JSON and .env
// all land on the SAME struct with the SAME values. That is a cross-module
// property, so it needs a shared fixture living outside all of them.
//
// The fixtures are deliberately identical in content and deliberately
// idiomatic in form: the INI file uses sections, the HCL file uses blocks, the
// YAML file uses nesting. Writing them all in one artificial style would prove
// only that a degenerate subset works.
//
// This package is part of the ROOT module and imports nothing beyond the
// standard library, so it adds no dependency to anyone. The cross-format proof
// that consumes it lives in examples/read-formats, which is where the format
// modules may be imported.
package mock

import "embed"

// FS holds the canonical configuration in every supported format.
//
// Embedded rather than read from disk so a consumer gets the fixtures by
// importing the package, with no working-directory assumption.
//
//go:embed config.yaml config.toml config.hcl config.ini config.properties config.json config.env
var FS embed.FS

// Fixture names one format and the file holding it.
type Fixture struct {
	Format string // "yaml", "ini", …
	File   string // the name inside FS
	Flat   bool   // true when the format has no nesting, so keys are dotted
}

// Fixtures is the canonical iteration order.
//
// A SLICE rather than a map, deliberately: a test that sweeps every format and
// prints as it goes must produce the same output twice, and Go randomises map
// iteration. It is also the list to extend when a format is added, so a new
// format joins the cross-format proof by touching one line.
var Fixtures = []Fixture{
	{Format: "yaml", File: "config.yaml"},
	{Format: "toml", File: "config.toml"},
	{Format: "hcl", File: "config.hcl"},
	{Format: "json", File: "config.json"},
	{Format: "ini", File: "config.ini", Flat: true},
	{Format: "properties", File: "config.properties", Flat: true},
	{Format: "env", File: "config.env", Flat: true},
}

// Server is the nested block every fixture declares.
//
// It carries FOUR tag vocabularies, and that is the honest cost of one struct
// reading every format rather than a trick:
//
//   - json       — JSON itself, and the shape structured sources merge onto.
//   - yaml, toml — each decoder reads its OWN tag and ignores the others.
//   - hcl        — required by gohcl, which will not bind a block without it.
//   - env        — used by INI, .properties and .env, which are FLAT: a
//     section becomes part of the key ("server.port"), so the parent field
//     carries `env:",prefix=server."` and the children stay unprefixed.
//
// The yaml and toml tags are NOT decoration. Both decoders fall back to the
// lowercased Go field name when no tag is present, so a single-word field like
// Port happens to work either way — and a multi-word one like MaxConns does
// not, because the fallback is "maxconns" and the document says "max_conns".
// It binds as zero, silently. examples/read-formats pins that trap with a test
// rather than leaving it to be discovered in production.
//
// A program that reads only one format needs only that format's vocabulary.
type Server struct {
	Port int    `json:"port" yaml:"port" toml:"port" hcl:"port,optional" env:"port"`
	Host string `json:"host" yaml:"host" toml:"host" hcl:"host,optional" env:"host"`
}

// Database is the second block, present so the fixtures prove more than one
// level of nesting binds.
type Database struct {
	URL string `json:"url" yaml:"url" toml:"url" hcl:"url,optional" env:"url"`

	// The multi-word field. Without the yaml and toml tags this binds as zero
	// from those two formats and nobody is told — see the type's doc comment.
	MaxConns int `json:"max_conns" yaml:"max_conns" toml:"max_conns" hcl:"max_conns,optional" env:"max_conns"`
}

// Config is the canonical struct every fixture binds to.
//
// Blocks are POINTERS because gohcl requires it for a block, and because a nil
// pointer is how cfgkit reports a section no source configured at all.
type Config struct {
	Service string `json:"service" yaml:"service" toml:"service" hcl:"service,optional" env:"service" default:"unset"`

	Server   *Server   `json:"server" yaml:"server" toml:"server" hcl:"server,block" env:",prefix=server."`
	Database *Database `json:"database" yaml:"database" toml:"database" hcl:"database,block" env:",prefix=database."`
}

// Want is the value every fixture must produce.
//
// Declared once so a test asserts against a single source of truth rather than
// repeating literals per format — repeated literals are how a format quietly
// drifts and the test still passes.
func Want() Config {
	return Config{
		Service:  "checkout",
		Server:   &Server{Port: 8080, Host: "0.0.0.0"},
		Database: &Database{URL: "postgres://db/checkout", MaxConns: 25},
	}
}
