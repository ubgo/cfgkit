# Capabilities

Three optional extension points for behaviour the core does not have: rewriting values, parsing types it does not know, and watching every resolution.

Each has an interface and a plain-function adapter, so injecting one never requires declaring a type.

| Capability | Changes | Runs |
|---|---|---|
| `Transformer` | what a value reads as | after lookup, before decoding — **chained** |
| `Decoder` | how text becomes a Go value | during decoding — **first claim wins** |
| `Observer` | nothing | after a field resolves — **all run** |

## The firewall

> **A capability may change what a value READS AS. It may never change WHICH SOURCE WON.**

Transformers rewrite values; they cannot redirect resolution. Decoders change how text becomes a value; they cannot claim a key. Observers see everything and change nothing.

**Why this rule exists.** Provenance is only worth having if it can be trusted at 3am. If a transformer could rewrite a field's origin, `Explain` would sometimes lie — and a diagnostic that lies occasionally is worse than one that does not exist, because people act on it.

It is enforced in code and pinned by two tests: one asserts a transformer can change a value but not its recorded source, and one loads the same configuration twice — bare, then with all three capabilities registered — and asserts every field's origin is byte-identical.

## `Transformer` — rewrite a value after lookup

**Use it when** values arrive in a form the field's type cannot parse, but a mechanical step can fix: ciphertext, base64, a value a platform wrapped in quotes, a legacy format you are migrating away from.

```go
cfgkit.WithTransform(func(key, value, source string) (string, error) {
	s, ok := strings.CutPrefix(value, "encrypted:")
	if !ok {
		return value, nil          // decline: pass through unchanged
	}
	return vault.Decrypt(s)
})
```

### It sees the key and the source

That is what makes it precise. The same key can be handled differently depending on where it came from:

```go
cfgkit.WithTransform(func(key, value, source string) (string, error) {
	if !strings.HasPrefix(source, "vault") {
		return value, nil          // only decrypt what came from Vault
	}
	return vault.Decrypt(value)
})
```

A local `.env` holding a development value is left alone; the same key from Vault is decrypted.

### Transformers chain

They run in registration order, each seeing the previous one's output:

```go
cfgkit.WithTransform(decryptIfEncrypted),   // runs first
cfgkit.WithTransform(expandTemplate),       // sees the decrypted value
cfgkit.WithTransform(strings.TrimSpace…),   // sees the expanded one
```

### Stateful transformers

`WithTransform` takes a closure. When the transformer needs to carry something — a client, a cache, a key ring — implement the interface and use `WithTransformer`:

```go
type vaultDecryptor struct{ client *vault.Client }

func (d *vaultDecryptor) Transform(key, value, source string) (string, error) { ... }

cfgkit.WithTransformer(&vaultDecryptor{client: c})
```

### Gotchas

**Decline by returning the value, not an error.** An error aborts the entire load. A transformer that does not apply to a value must return it unchanged — returning an error means "this configuration is broken", which is a much stronger claim.

**Transformers see every field, not only the ones you care about.** Guard on the prefix, the key, or the source before doing work. A transformer that calls a network service unconditionally makes every field a round-trip.

**They run before decoding, so you get the raw string.** Transform text, not typed values. If you want to adjust a parsed value, do it in `Derive` instead.

**They cannot see the field's type or tags.** A transformer works on `(key, value, source)` only. If the rule depends on the destination type, you want a `Decoder`.

**A transformer applies to defaults too — no, actually it does not.** Only values a source supplied pass through the chain. A field left on its compiled-in default is never transformed, because there is no source to attribute and nothing arrived to rewrite.

## `Decoder` — parse a type the core does not know

**Use it when** the type is not yours to change — a struct from a third-party package with no unmarshaler — or when a type must be parsed differently *in configuration* than everywhere else.

```go
cfgkit.WithDecoder(cfgkit.DecoderFunc(func(typ reflect.Type, raw string) (any, bool, error) {
	if typ != reflect.TypeOf(pgx.ConnConfig{}) {
		return nil, false, nil        // decline
	}
	c, err := pgx.ParseConfig(raw)
	if err != nil {
		return nil, true, err         // claim it, reject the value
	}
	return *c, true, nil              // claim it, here is the value
}))
```

Three return shapes, and the middle value is the important one:

| Return | Meaning |
|---|---|
| `(value, true, nil)` | this type is mine, here is the parsed value |
| `(nil, false, nil)` | not mine — try the next decoder, then the core |
| `(nil, true, err)` | mine, and this value is invalid |

### If the type is yours, do not use a decoder

Implement `encoding.TextUnmarshaler` on the type instead. The parsing rule then travels **with the type**, so every other package that reads it gets the same behaviour, and the rule cannot be forgotten at a second call site. A decoder is for types you cannot change.

### Decoders override the core

They are consulted **before** the built-in type set and before the `TextUnmarshaler` hatch. That ordering is deliberate: overriding is the reason to register one.

```go
// Treat a bare number as minutes rather than rejecting it.
cfgkit.WithDecoder(cfgkit.DecoderFunc(func(typ reflect.Type, raw string) (any, bool, error) {
	if typ != reflect.TypeOf(time.Duration(0)) {
		return nil, false, nil
	}
	...
}))
```

### Gotchas

**Decide by TYPE, not by value.** The predicate is probed with an **empty string** to discover which types a decoder claims, before any value exists. A decoder that inspects `raw` to decide whether to claim will behave unpredictably — it may decline during the probe and claim later, or the reverse.

**A claimed struct becomes a leaf.** `cfgkit` normally walks into a struct and binds its fields individually. A struct a decoder claims is bound from **one key** instead. That is required — otherwise the walker would descend into it and the decoder would never be reached — but it means claiming a type changes how the whole section is configured.

**Return something assignable.** A decoder that claims `Bespoke` and returns an `int` gets a clear error rather than a reflect panic, but it is still a bug in the decoder.

**Later registrations win.** Decoders are consulted in reverse registration order, so a decoder added later overrides an earlier one for the same type.

**Decoders apply to `default:` tag values too.** The tag is parsed by the same path, so a decoder that changes how a type is read also changes how its default is read. That is usually what you want; it is surprising if you did not expect it.

## `Observer` — watch every resolution

**Use it when** you want to record or count something without changing behaviour: auditing which secrets were read and from where, warning at startup about fields still on their compiled-in defaults, or reporting deprecated keys still in use.

```go
cfgkit.WithObserver(cfgkit.ObserverFunc(func(f cfgkit.Field) {
	if f.Secret {
		audit.Record(f.Path, f.Source)      // name and origin, never the value
	}
}))
```

A practical one — surface configuration that nobody set:

```go
cfgkit.WithObserver(cfgkit.ObserverFunc(func(f cfgkit.Field) {
	if f.Secret && f.Source == "default" {
		log.Printf("WARNING: %s is using its built-in default", f.Path)
	}
	if strings.Contains(f.Source, "deprecated") {
		log.Printf("MIGRATE: %s still arrives via a former key name", f.Path)
	}
}))
```

### Gotchas

**Observers receive secret values UNMASKED.** An audit sink needs the real value, so `cfgkit` hands it over and lets the observer decide. **An observer that logs must check `Field.Secret` itself** — the library cannot know whether your sink is a safe place for a credential. This is the single place where a secret leaves the package unmasked, and it does so deliberately.

**Observers cannot fail.** `ObserveResolve` returns nothing. An observer that needs to report a problem should record it and let your code check afterwards; it cannot abort a load, because a diagnostic hook that can break startup is a liability.

**They fire for defaulted fields too.** A field no source supplied is still observed, with `Source` set to `default`. That is deliberate — "still on the built-in default" is exactly what an audit wants to know.

**Order is registration order**, and every registered observer runs. None can prevent another from running.

**Do not do slow work inline.** An observer runs once per field during startup. Buffer and flush afterwards rather than making a network call per field.

## Choosing between them

| You want to… | Use |
|---|---|
| decrypt, decode base64, unwrap a value | `Transformer` |
| parse a third-party type | `Decoder` |
| change how a type you own is parsed | `encoding.TextUnmarshaler` on the type |
| compute a value from other fields | `Derive`, not a capability |
| reject a value | `Validate`, not a capability |
| record or count without changing anything | `Observer` |
| supply values from a backend | a `Source`, not a capability |

The last two rows matter: capabilities are for *how a value is read*. Where a value comes from is a [source](sources.md), and whether it is acceptable is [validation](validation.md).
