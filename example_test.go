package cfgkit_test

import (
	"fmt"
	"os"
	"time"

	"github.com/ubgo/cfgkit"
)

// ExampleConfig is the struct the examples below load. Every example in this
// file is verified by `go test`: its Output block must match byte for byte, so
// the snippets in README.md cannot drift from what the code actually prints.
type ExampleConfig struct {
	Port    int           `env:"PORT" default:"8080" doc:"HTTP listen port"`
	Timeout time.Duration `env:"TIMEOUT" default:"15s" doc:"Request timeout"`
	DBURL   string        `env:"DATABASE_URL,required" doc:"Postgres connection string"`
	APIKey  string        `env:"API_KEY" secret:"true" doc:"Upstream API key"`
}

// Example_explain shows the provenance table: every field, its value, and the
// source that set it. This is the question no other Go config library answers.
func Example_explain() {
	_, res, err := cfgkit.Load[ExampleConfig](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{
			"DATABASE_URL": "postgres://localhost/dev",
			"API_KEY":      "super-secret-value",
		}),
		cfgkit.FromMap(map[string]string{"PORT": "9001"}),
	))
	if err != nil {
		fmt.Println(err)
		return
	}
	_ = res.Explain(os.Stdout)

	// Output:
	// FIELD    KEY           VALUE                     SOURCE
	// APIKey   API_KEY       ••••••                    map
	// DBURL    DATABASE_URL  postgres://localhost/dev  map
	// Port     PORT          9001                      map
	// Timeout  TIMEOUT       15s                       default
}

// Example_secretsAreAbsent shows that masking is structural: the value is not
// styled out, it never enters the output at all. Pass Reveal to opt in.
func Example_secretsAreAbsent() {
	src := cfgkit.FromMap(map[string]string{
		"DATABASE_URL": "postgres://localhost/dev",
		"API_KEY":      "super-secret-value",
	})

	_, masked, _ := cfgkit.Load[ExampleConfig](cfgkit.WithSources(src))
	b, _ := masked.JSON()
	fmt.Println("default:", string(b))

	_, revealed, _ := cfgkit.Load[ExampleConfig](cfgkit.WithSources(src), cfgkit.Reveal())
	b, _ = revealed.JSON()
	fmt.Println("reveal: ", string(b))

	// Output:
	// default: {"mode":"dev","fields":[{"path":"APIKey","key":"API_KEY","value":"••••••","source":"map","secret":true},{"path":"DBURL","key":"DATABASE_URL","value":"postgres://localhost/dev","source":"map","secret":false},{"path":"Port","key":"PORT","value":"8080","source":"default","secret":false},{"path":"Timeout","key":"TIMEOUT","value":"15s","source":"default","secret":false}]}
	// reveal:  {"mode":"dev","fields":[{"path":"APIKey","key":"API_KEY","value":"super-secret-value","source":"map","secret":true},{"path":"DBURL","key":"DATABASE_URL","value":"postgres://localhost/dev","source":"map","secret":false},{"path":"Port","key":"PORT","value":"8080","source":"default","secret":false},{"path":"Timeout","key":"TIMEOUT","value":"15s","source":"default","secret":false}]}
}

// Example_check shows the CI gate: the full pipeline runs, every problem is
// reported, and no application is constructed.
func Example_check() {
	err := cfgkit.Check[ExampleConfig](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"PORT": "eighty"}),
	))
	fmt.Println(err)

	// Output:
	// cfgkit: 2 problem(s):
	// Port (PORT from map): "eighty" is not a valid int
	// DBURL (DATABASE_URL) is required but no source supplied it
}

// Example_document shows the generated contract file. Secrets are emitted with
// an empty value, because the file is committed.
func Example_document() {
	// A write failure here would make the Output comparison below fail anyway,
	// so the error is discarded explicitly rather than handled twice.
	_ = cfgkit.Document[ExampleConfig](os.Stdout)

	// Output:
	// # Upstream API key
	// # optional · secret — do not commit a real value
	// API_KEY=
	//
	// # Postgres connection string
	// # REQUIRED
	// DATABASE_URL=
	//
	// # HTTP listen port
	// # optional
	// PORT=8080
	//
	// # Request timeout
	// # optional
	// TIMEOUT=15s
}

// Example_zeroInput shows the property everything else hangs off: no sources,
// no environment, and the configuration is still complete and usable.
func Example_zeroInput() {
	type Server struct {
		Host string `env:"HOST" default:"localhost"`
		Port int    `env:"PORT" default:"8080"`
	}

	cfg, _, err := cfgkit.Load[Server]()
	fmt.Printf("%s:%d err=%v\n", cfg.Host, cfg.Port, err)

	// Output:
	// localhost:8080 err=<nil>
}
