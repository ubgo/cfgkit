// Command read-environment loads configuration from environment variables.
//
// It shows the two ways to read them and why the prefixed form exists: one
// process often hosts several components, and without a namespace they fight
// over short names like PORT and TIMEOUT.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/ubgo/cfgkit"
)

// Config binds the process-wide variables.
type Config struct {
	Port  int    `env:"PORT" default:"8080"`
	Level string `env:"LOG_LEVEL" default:"info"`
}

// WorkerConfig binds a component's OWN variables, which are namespaced in the
// environment but plain in the struct — the prefix is stripped before binding.
type WorkerConfig struct {
	Concurrency int    `env:"CONCURRENCY" default:"4"`
	Queue       string `env:"QUEUE" default:"default"`
}

func main() {
	if err := run(os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer) error {
	cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(cfgkit.FromEnviron()))
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "server: port=%d level=%s\n", cfg.Port, cfg.Level)
	if err := res.Explain(w); err != nil {
		return err
	}

	// The worker reads WORKER_CONCURRENCY and WORKER_QUEUE, but its struct
	// says CONCURRENCY and QUEUE. Stripping the prefix is what lets the same
	// struct bind from a namespaced environment and from a plain .env file.
	_, _ = fmt.Fprintln(w)
	worker, wres, err := cfgkit.Load[WorkerConfig](
		cfgkit.WithSources(cfgkit.FromPrefixedEnviron("WORKER_")),
	)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "worker: concurrency=%d queue=%s\n", worker.Concurrency, worker.Queue)
	return wres.Explain(w)
}
