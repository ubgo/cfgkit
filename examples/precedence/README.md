# precedence — four layers, and who won

```sh
go run ./precedence
```

The question layered configuration actually raises in production is not *what is this value* but **which layer decided it**. This example stacks four and answers that for every field.

## The stack

```go
cfgkit.Load[Config](cfgkit.WithSources(
	// 1. struct tags — always first, never listed
	cfgkit.FromFiles("base.env"),                          // 2. the image's baseline
	cfgkit.FromFiles("prod.env"),                          // 3. the overlay
	cfgkit.FromMap(map[string]string{"PORT": "9090"}),     // 4. an operator override
))
```

Precedence is **positional**: the value of a field is whatever the last source to claim its key said. Nothing is reordered internally, no source is special, and no source outranks another for being a different kind. The order you read in the code is the order that runs.

## What it prints

```
checkout :9090 level=warn new-checkout=true

FIELD               KEY                   VALUE     SOURCE
AppName             APP_NAME              checkout  file:base.env
FeatureNewCheckout  FEATURE_NEW_CHECKOUT  true      file:prod.env
LogLevel            LOG_LEVEL             warn      file:prod.env
Port                PORT                  9090      map

keys no field claimed:
  DEPLOY_REGION (from file:prod.env)
```

All four layers decided something, and the `SOURCE` column says which. That is the output to reach for when someone asks why the port is 9090 in production and 8080 on their laptop — no bisecting a config tree, no guessing.

## The overlay sets only what differs

`prod.env` never mentions `APP_NAME`, so the baseline keeps it. That is the whole reason layering beats a full copy per environment: an overlay that restated every key would drift from the baseline the first time one of them changed, and the drift would be invisible until something broke.

## An unbound key is reported, never fatal

`DEPLOY_REGION` binds no field. It is surfaced through `Result.Unknown()` and the program still boots.

Strict-fail is tempting and wrong here. One `.env` file routinely serves several audiences — application config beside deploy-pipeline variables, secrets the CI runner injects, keys another service reads from the same file. Refusing to start because a key was meant for somebody else would break the common case in order to catch a typo.

So the typo is *reported*, which catches it, and the boot proceeds, which does not punish a normal layout. If you want it fatal, `len(res.Unknown()) > 0` is one line in your own startup path — that decision belongs to the program, not the library.

Note which source it names: `file:prod.env`. Unknown keys carry their origin, so a report from a many-layered load still points at one file.

## Next

- [`read-file`](../read-file) — one file, in detail
- [`provenance`](../provenance) — reading `Result` in code rather than printing it
- [`validation`](../validation) — failing on purpose, before the program boots
