package cfgkit_test

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ubgo/cfgkit"
)

// Every example in this file backs a snippet in docs/. The // Output: blocks
// are verified by `go test`, so a documented behaviour that changes fails the
// build instead of quietly becoming fiction.

// ExamplePrecedence shows that the LAST source claiming a key wins, and that a
// key only an earlier source sets still applies.
func Example_precedence() {
	type Config struct {
		Port int    `env:"PORT" default:"8080"`
		Host string `env:"HOST" default:"localhost"`
		Name string `env:"NAME" default:"app"`
	}

	cfg, res, _ := cfgkit.Load[Config](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"PORT": "1111", "HOST": "from-first"}),
		cfgkit.FromMap(map[string]string{"PORT": "2222"}),
	))

	fmt.Printf("Port=%d Host=%s Name=%s\n", cfg.Port, cfg.Host, cfg.Name)
	for _, f := range res.Fields() {
		fmt.Printf("%-5s %s\n", f.Path, f.Source)
	}

	// Output:
	// Port=2222 Host=from-first Name=app
	// Host  map
	// Name  default
	// Port  map
}

// Example_fileValue shows the Docker and Kubernetes secret convention: the
// variable holds a PATH, and the value is the file's contents.
func Example_fileValue() {
	type Config struct {
		Password string `env:"DB_PASSWORD_FILE,file" secret:"true"`
	}

	// Setup failures panic rather than being discarded: an Example that
	// silently proceeds on a missing fixture reports a pass for the wrong
	// reason, which is worse than a crash.
	dir, err := os.MkdirTemp("", "cfgkit")
	if err != nil {
		panic(err)
	}
	// Cleanup failure is not actionable and must not mask the real result.
	defer func() { _ = os.RemoveAll(dir) }()

	path := filepath.Join(dir, "pw")
	if err := os.WriteFile(path, []byte("s3cret\n"), 0o600); err != nil { // note the trailing newline
		panic(err)
	}

	cfg, _, err := cfgkit.Load[Config](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"DB_PASSWORD_FILE": path}),
	))
	fmt.Printf("%q err=%v\n", cfg.Password, err)

	// Output:
	// "s3cret" err=<nil>
}

// Example_formerKeyName shows that renaming a key keeps old deployments
// working, and that the provenance flags the deprecated name.
func Example_formerKeyName() {
	type Config struct {
		APIKey string `env:"HYPERDX_API_KEY" was:"HYPERDX_KEY"`
	}

	cfg, res, _ := cfgkit.Load[Config](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"HYPERDX_KEY": "from-old-name"}),
	))
	fmt.Println(cfg.APIKey)
	fmt.Println(res.Fields()[0].Source)

	// Output:
	// from-old-name
	// map (deprecated key HYPERDX_KEY)
}

// Example_requiredVsNotEmpty shows that a missing key and a declared-but-empty
// value are different failures, because they need different fixes.
func Example_requiredVsNotEmpty() {
	type Config struct {
		Token string `env:"TOKEN,required"`
		Name  string `env:"NAME,notempty"`
	}

	fmt.Println(cfgkit.Check[Config](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"NAME": "x"}),
	)))
	fmt.Println(cfgkit.Check[Config](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"TOKEN": "t", "NAME": ""}),
	)))

	// Output:
	// cfgkit: 1 problem(s):
	// Token (TOKEN) is required but no source supplied it
	// cfgkit: 1 problem(s):
	// Name (NAME) is required but resolved to an empty value
}

// ServeMode is the discriminant of a tagged union — a real Go type, so a
// comparison against it cannot be a mistyped string.
type ServeMode string

const (
	ServeCloudflare ServeMode = "cloudflare"
	ServeDirect     ServeMode = "direct"
)

type PurgeConfig struct {
	Token string `env:"TOKEN"`
}

type Serving struct {
	Kind       ServeMode    `env:"SERVE_KIND" default:"direct"`
	Cloudflare *PurgeConfig `env:",prefix=CF_"`
}

func (s *Serving) Validate() error {
	return cfgkit.RequiredWhen(
		"Cloudflare", s.Cloudflare != nil,
		"kind=cloudflare", s.Kind == ServeCloudflare,
	)
}

// Example_conditionalRule shows one rule call asserting BOTH directions of a
// discriminated-union invariant.
func Example_conditionalRule() {
	missing := cfgkit.Check[Serving](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"SERVE_KIND": "cloudflare"}),
	))
	fmt.Println(missing)

	forbidden := cfgkit.Check[Serving](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"SERVE_KIND": "direct", "CF_TOKEN": "t"}),
	))
	fmt.Println(forbidden)

	ok := cfgkit.Check[Serving](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"SERVE_KIND": "cloudflare", "CF_TOKEN": "t"}),
	))
	fmt.Println("valid:", ok)

	// Output:
	// cfgkit: 1 problem(s):
	// Cloudflare: is required when kind=cloudflare, but it was not set (required_when)
	// cfgkit: 1 problem(s):
	// Cloudflare: must not be set unless kind=cloudflare (required_when)
	// valid: <nil>
}

// Example_modeStrictness shows the rule that makes zero-config safe: the value
// that lets a fresh clone run must refuse to boot in production.
func Example_modeStrictness() {
	const placeholder = "__CHANGE_ME__"

	fmt.Println("dev: ", cfgkit.NotWeakSecret(cfgkit.ModeDev, "Key", placeholder))
	fmt.Println("prod:", cfgkit.NotWeakSecret(cfgkit.ModeProd, "Key", placeholder))

	// Output:
	// dev:  <nil>
	// prod: Key: is still a placeholder in mode=prod; set a real value (not_weak_secret)
}

// Example_derive shows a value computed from other fields, and that Validate
// sees the derived result.
func Example_derive() {
	cfg, _, err := cfgkit.Load[derived](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"DB_HOST": "db.internal"}),
	))
	fmt.Println(cfg.DSN, "err:", err)

	// Output:
	// postgres://db.internal:5432/app err: <nil>
}

type derived struct {
	Host string `env:"DB_HOST"`
	Port int    `env:"DB_PORT"`
	DSN  string `env:"-"`
}

func (d *derived) Defaults() { d.Host, d.Port = "localhost", 5432 }

func (d *derived) Derive() error {
	d.DSN = fmt.Sprintf("postgres://%s:%d/app", d.Host, d.Port)
	return nil
}

func (d *derived) Validate() error {
	return cfgkit.Range("Port", d.Port, 1, 65535)
}

// Example_structuredAndFlat shows both source kinds in one chain: nested data
// matched by json tags, flat keys matched by env tags, later source winning.
func Example_structuredAndFlat() {
	type HyperDX struct {
		LogsSourceID string `env:"LOGS_SOURCE_ID" json:"logsSourceId"`
		APIKey       string `env:"API_KEY"        json:"apiKey"`
	}
	type Config struct {
		HyperDX HyperDX `env:",prefix=HYPERDX_" json:"hyperdx"`
	}

	cfg, res, _ := cfgkit.Load[Config](cfgkit.WithSources(
		cfgkit.FromJSON([]byte(`{"hyperdx":{"logsSourceId":"from-json","apiKey":"from-json"}}`)),
		cfgkit.FromMap(map[string]string{"HYPERDX_API_KEY": "from-env"}),
	))

	fmt.Println(cfg.HyperDX.LogsSourceID, cfg.HyperDX.APIKey)
	for _, f := range res.Fields() {
		fmt.Printf("%-22s %-24s %s\n", f.Path, f.Key, f.Source)
	}

	// Output:
	// from-json from-env
	// HyperDX.APIKey         HYPERDX_API_KEY          map
	// HyperDX.LogsSourceID   HYPERDX_LOGS_SOURCE_ID   json
}

// Example_flagDefaultNeverWins shows the rule viper gets wrong: a flag the user
// did not type contributes nothing, so a config source still wins.
func Example_flagDefaultNeverWins() {
	type Config struct {
		Port int `env:"PORT" flag:"port" default:"8080"`
	}

	fs := flag.NewFlagSet("app", flag.ContinueOnError)
	fs.Int("port", 9999, "port") // declared with a default, never typed
	if err := fs.Parse(nil); err != nil {
		panic(err)
	}

	cfg, res, _ := cfgkit.Load[Config](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"PORT": "3000"}),
		cfgkit.FromFlagSet(fs), // highest precedence
	))
	fmt.Printf("Port=%d from=%s\n", cfg.Port, res.Fields()[0].Source)

	// Output:
	// Port=3000 from=map
}

// Example_customSource shows that any flat backend is a closure, with its
// dependency staying in the caller.
func Example_customSource() {
	type Config struct {
		Secret string `env:"APP_SECRET" secret:"true"`
	}

	// Pre-load once: Lookup is called per field, so a remote store must cache.
	store := map[string]string{"APP_SECRET": "from-vault"}
	src := cfgkit.SourceFunc("vault", func(key string) (string, bool, error) {
		v, ok := store[key]
		return v, ok, nil
	})

	_, res, _ := cfgkit.Load[Config](cfgkit.WithSources(src), cfgkit.Reveal())
	fmt.Printf("%s=%s from %s\n", res.Fields()[0].Key, res.Fields()[0].Value, res.Fields()[0].Source)

	// Output:
	// APP_SECRET=from-vault from vault
}

// Example_sourceFailureAborts shows that an unreachable backend is never a
// miss: it aborts, so a deploy cannot proceed with an empty password.
func Example_sourceFailureAborts() {
	type Config struct {
		Secret string `env:"APP_SECRET"`
	}

	down := cfgkit.SourceFunc("vault", func(string) (string, bool, error) {
		return "", false, errors.New("connection refused")
	})

	err := cfgkit.Check[Config](cfgkit.WithSources(down))
	var se *cfgkit.SourceError
	fmt.Println(errors.As(err, &se), err)

	// Output:
	// true cfgkit: 1 problem(s):
	// source vault failed for APP_SECRET: connection refused
}

// Example_timeout documents the duration and slice decoders together.
func Example_slicesAndDurations() {
	type Config struct {
		Timeout time.Duration `env:"TIMEOUT" default:"30s"`
		Origins []string      `env:"ORIGINS"`
		Ports   []int         `env:"PORTS" delim:";"`
	}

	cfg, _, _ := cfgkit.Load[Config](cfgkit.WithSources(cfgkit.FromMap(map[string]string{
		"TIMEOUT": "2m30s",
		"ORIGINS": "https://a.test, https://b.test",
		"PORTS":   "80;443",
	})))
	fmt.Println(cfg.Timeout, cfg.Origins, cfg.Ports)

	// Output:
	// 2m30s [https://a.test https://b.test] [80 443]
}

// SMTPSection is an optional feature: nil means this app does not send email.
type SMTPSection struct {
	Host string `env:"HOST"`
	User string `env:"USER"`
	Port int    `env:"PORT" default:"587"`
}

type AppWithOptional struct {
	Port int          `env:"APP_PORT" default:"8080"`
	SMTP *SMTPSection `env:",prefix=SMTP_"`
}

// Example_optionalSection shows the rule from CONFIG_SPEC §6.4: a *Struct is
// nil unless a source set something beneath it, so the ordinary Go nil check
// means "the operator configured this feature".
func Example_optionalSection() {
	// 1. Nobody configured email.
	none, _, _ := cfgkit.Load[AppWithOptional]()
	fmt.Printf("nothing set:      SMTP == nil? %v\n", none.SMTP == nil)

	// 2. One field is enough — setting SMTP_HOST states intent.
	one, _, _ := cfgkit.Load[AppWithOptional](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"SMTP_HOST": "mail.example.com"}),
	))
	fmt.Printf("SMTP_HOST set:    SMTP == nil? %v  Host=%q Port=%d\n",
		one.SMTP == nil, one.SMTP.Host, one.SMTP.Port)

	// 3. A default alone is NOT intent. SMTPSection.Port has default:"587",
	//    and that does not bring the section into existence.
	other, _, _ := cfgkit.Load[AppWithOptional](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"APP_PORT": "9000"}),
	))
	fmt.Printf("only APP_PORT:    SMTP == nil? %v\n", other.SMTP == nil)

	// Output:
	// nothing set:      SMTP == nil? true
	// SMTP_HOST set:    SMTP == nil? false  Host="mail.example.com" Port=587
	// only APP_PORT:    SMTP == nil? true
}

// CacheSection's defaults are a complete working setup, so it opts into always
// being allocated.
type CacheSection struct {
	TTL  time.Duration `env:"TTL" default:"5m"`
	Size int           `env:"SIZE" default:"1000"`
}

type AppWithInit struct {
	Cache *CacheSection `env:",prefix=CACHE_,init"`
}

// Example_optionalSectionInit shows the override: a caller reads cfg.Cache
// without a nil guard.
func Example_optionalSectionInit() {
	cfg, _, _ := cfgkit.Load[AppWithInit]()
	fmt.Printf("Cache == nil? %v  TTL=%v Size=%d\n", cfg.Cache == nil, cfg.Cache.TTL, cfg.Cache.Size)

	// Output:
	// Cache == nil? false  TTL=5m0s Size=1000
}

// Example_unknownKeys shows the mirror of Explain: not "where did this value
// come from" but "why did my value go nowhere".
func Example_unknownKeys() {
	type Config struct {
		DatabaseURL string `env:"DATABASE_URL" default:"postgres://localhost/dev"`
		Port        int    `env:"PORT" default:"8080"`
	}

	// A .env file with a typo on the first key.
	_, res, _ := cfgkit.Load[Config](cfgkit.WithSources(cfgkit.FromMap(map[string]string{
		"DATABAS_URL": "postgres://prod-db.internal/app", // typo: missing the E
		"PORT":        "9000",
	})))

	for _, u := range res.Unknown() {
		fmt.Println(u)
	}

	// Output:
	// DATABAS_URL (from map) matched no field
}

// Example_unknownKeysIgnoresEnviron shows why FromEnviron is excluded: its key
// set is the whole machine, so reporting it would bury the line that matters.
func Example_unknownKeysIgnoresEnviron() {
	type Config struct {
		Port int `env:"PORT" default:"8080"`
	}

	_, res, _ := cfgkit.Load[Config](cfgkit.WithSources(cfgkit.FromEnviron()))
	fmt.Printf("unknown keys reported: %d\n", len(res.Unknown()))

	// Output:
	// unknown keys reported: 0
}

// Example_zeroValuesOverrideDefaults shows the rule that makes a default of
// `true` disableable: a source wins even when its value is the type's zero.
func Example_zeroValuesOverrideDefaults() {
	type Config struct {
		Host    string   `env:"Z_HOST" default:"localhost"`
		Debug   bool     `env:"Z_DEBUG" default:"true"`
		Origins []string `env:"Z_ORIGINS" default:"a,b"`
	}

	// The operator deliberately clears the host, turns debug off, and allows
	// no origins. Every one of these is a zero value.
	cfg, _, _ := cfgkit.Load[Config](cfgkit.WithSources(cfgkit.FromMap(map[string]string{
		"Z_HOST":    "",
		"Z_DEBUG":   "false",
		"Z_ORIGINS": "",
	})))
	fmt.Printf("Host=%q Debug=%v Origins=%v\n", cfg.Host, cfg.Debug, cfg.Origins)

	// A key NO source mentions keeps its default — absent and empty differ.
	other, _, _ := cfgkit.Load[Config](cfgkit.WithSources(cfgkit.FromMap(map[string]string{})))
	fmt.Printf("Host=%q Debug=%v Origins=%v\n", other.Host, other.Debug, other.Origins)

	// Output:
	// Host="" Debug=false Origins=[]
	// Host="localhost" Debug=true Origins=[a b]
}

// Example_mapFields shows the one case a map is for: the KEY NAMES are not
// known when the struct is written, so a new entry is added by editing a .env
// file rather than the Go source.
func Example_mapFields() {
	type Config struct {
		// Feature flags: nobody can list them at compile time.
		Flags map[string]string `env:"FLAGS"`
		// Per-tenant limits, decoded through the same decoder an int field uses.
		Limits map[string]int `env:"LIMITS"`
		// Timeouts, likewise — any supported type works as the value.
		Timeouts map[string]time.Duration `env:"TIMEOUTS"`
	}

	cfg, _, _ := cfgkit.Load[Config](cfgkit.WithSources(cfgkit.FromMap(map[string]string{
		"FLAGS":    "new-checkout:on,dark-mode:off",
		"LIMITS":   "acme:1000,globex:500",
		"TIMEOUTS": "read:30s,write:1m",
	})))

	fmt.Printf("new-checkout=%s dark-mode=%s\n", cfg.Flags["new-checkout"], cfg.Flags["dark-mode"])
	fmt.Printf("acme=%d globex=%d\n", cfg.Limits["acme"], cfg.Limits["globex"])
	fmt.Printf("read=%s write=%s\n", cfg.Timeouts["read"], cfg.Timeouts["write"])

	// Output:
	// new-checkout=on dark-mode=off
	// acme=1000 globex=500
	// read=30s write=1m0s
}

// Example_mapValuesMayContainColons is the gotcha worth memorising: only the
// FIRST separator in an entry splits key from value, so a connection string
// survives intact.
func Example_mapValuesMayContainColons() {
	type Config struct {
		DSNs map[string]string `env:"DSNS"`
	}

	cfg, _, _ := cfgkit.Load[Config](cfgkit.WithSources(cfgkit.FromMap(map[string]string{
		"DSNS": "primary:postgres://user@db1:5432/app,cache:redis://cache:6379",
	})))

	fmt.Println(cfg.DSNs["primary"])
	fmt.Println(cfg.DSNs["cache"])

	// Output:
	// postgres://user@db1:5432/app
	// redis://cache:6379
}

// Example_defaultSources shows the conventional chain replacing the twelve
// lines every service otherwise writes identically.
func Example_defaultSources() {
	// A directory standing in for the working directory.
	dir, _ := os.MkdirTemp("", "cfgkit")
	defer func() { _ = os.RemoveAll(dir) }()
	_ = os.WriteFile(filepath.Join(dir, ".env"), []byte(
		"APP_ENV=production\nEX_HOST=from-base\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, ".env.prod"), []byte(
		"EX_HOST=from-prod-file\n"), 0o600)

	type Config struct {
		Host string `env:"EX_HOST" default:"localhost"`
		Port int    `env:"EX_PORT" default:"8080"`
	}

	// One call: the .env chain for the resolved mode, then the environment.
	cfg, res, _ := cfgkit.Load[Config](cfgkit.DefaultSourcesIn(dir))

	// APP_ENV lives in a FILE, and it still selects the mode — which then
	// selects which .env.<mode> file is loaded.
	fmt.Println("mode:", res.Mode())
	fmt.Println("host:", cfg.Host)
	fmt.Println("port:", cfg.Port)
	for _, f := range res.Files() {
		fmt.Println("consulted:", filepath.Base(f))
	}

	// Output:
	// mode: prod
	// host: from-prod-file
	// port: 8080
	// consulted: .env
	// consulted: .env.local
	// consulted: .env.prod
	// consulted: .env.prod.local
}

// Example_watcher shows the reload contract: a bad edit is reported and the
// process keeps running on the configuration it already had.
func Example_watcher() {
	dir, _ := os.MkdirTemp("", "cfgkit")
	defer func() { _ = os.RemoveAll(dir) }()
	env := filepath.Join(dir, ".env")
	write := func(body string) { _ = os.WriteFile(env, []byte(body), 0o600) }

	type Config struct {
		Host string `env:"EX_HOST" default:"localhost"`
		Port int    `env:"EX_PORT" default:"8080"`
	}

	write("EX_HOST=first\nEX_PORT=9000\n")

	// The sources are built INSIDE the function, which is what makes a reload
	// re-read the file: FromFiles reads at construction.
	w, err := cfgkit.NewWatcher[Config](func() []cfgkit.Option {
		return []cfgkit.Option{cfgkit.WithSources(cfgkit.FromFiles(env))}
	})
	if err != nil {
		panic(err)
	}
	fmt.Printf("gen %d: %s:%d\n", w.Generation(), w.Current().Host, w.Current().Port)

	// A good edit is published.
	write("EX_HOST=second\nEX_PORT=9001\n")
	fmt.Println("reload:", w.Reload())
	fmt.Printf("gen %d: %s:%d\n", w.Generation(), w.Current().Host, w.Current().Port)

	// A bad edit is refused, and the process keeps the configuration it had.
	write("EX_HOST=third\nEX_PORT=not-a-number\n")
	fmt.Println("reload:", w.Reload() != nil)
	fmt.Printf("gen %d: %s:%d\n", w.Generation(), w.Current().Host, w.Current().Port)

	// Output:
	// gen 1: first:9000
	// reload: <nil>
	// gen 2: second:9001
	// reload: true
	// gen 2: second:9001
}
