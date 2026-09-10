# cfgkit/contrib/source-kiln

**Support level: supported** — 96.6% covered, and the covering includes real age encryption: the suite generates an identity, writes a `kiln.toml`, encrypts a fixture with kiln itself, and reads it back. See [the catalogue](../../docs/catalogue.md#support-levels).

Read secrets from a [kiln](https://github.com/thunderbottom/kiln)-encrypted environment file as a cfgkit source.

```go
import kiln "github.com/ubgo/cfgkit/contrib/source-kiln"

cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
	cfgkit.FromFiles(".env"),                 // non-secret defaults
	kiln.Env("kiln.toml", "production"),      // the encrypted half
	cfgkit.FromEnviron(),                     // still wins over everything
))
```

## Why this one is worth an adapter

Every other secret source in this catalogue answers the same question — *where do I put the secrets so they are not in the repository?* — and every answer costs the same thing: now the configuration lives in two places, and a developer cloning the repository gets half of it.

kiln answers differently. The encrypted file **is committed**, next to the code, and only the age or SSH identities named in `kiln.toml` can read it. There is one place to look, one thing to clone, and the review history of a secret rotation is a normal diff. That is a genuinely different trade, not a different vendor, which is the bar for adding a module here.

## It carries a dependency, on purpose

Six sources in this catalogue take no dependency at all, because reading a remote secret is one HTTP GET and an SDK is a large price for that. This one takes `github.com/thunderbottom/kiln` — and the reason is the rule the catalogue states: **use the platform's own mechanism when the credential is a file or a header; use the vendor's library when the hard part is not the transport.**

Here there is no transport. The hard part is age decryption and the access-control model, and a hand-rolled version of either would be a worse copy of something that already exists — with a failure mode, in the cryptography case, that does not announce itself.

## Keys

By default the key is **discovered** the way the `kiln` CLI discovers it — `~/.kiln/kiln.key`, then `~/.ssh/id_ed25519`, and so on. A developer who can already run `kiln` needs no extra configuration here.

CI usually cannot rely on that, so name the key:

```go
kiln.Env("kiln.toml", "production", kiln.WithKeyPath(os.Getenv("KILN_KEY_PATH")))
```

## Prefixes strip

```go
kiln.Env("kiln.toml", "production", kiln.WithPrefix("API_"))
// API_PORT in the file  →  binds to `env:"PORT"`
```

This differs from koanf's provider, which filters on the prefix but keeps it. Stripping is deliberate: the point of a prefix is that one encrypted file can serve several components without every struct repeating the namespace in its tags. It is the same thing `cfgkit.FromPrefixedEnviron` does in the core, and the two meaning different things would be worse than either meaning.

Stripping is a rename, not an alias — after `WithPrefix("API_")`, `PORT` resolves and `API_PORT` does not. An alias would let two struct fields bind the same value under different names and drift apart.

## Failures are errors, never misses

An identity that `kiln.toml` does not grant access to gets an **error**, not an empty result. That distinction is the entire safety argument for the adapter: a source that reported "not found" when it meant "not allowed" would let a deploy proceed with an empty database password, and nothing downstream could tell the difference.

An environment that decrypts fine but contains nothing is also an error by default — you named this file, so finding it empty is a deployment mistake. `kiln.Optional()` opts out, and opts out of *that only*; it never suppresses a decryption failure.

## Decryption happens once

At construction, not per key. `Lookup` runs once per bound field, so decrypting per key would unlock the identity as many times as the struct has fields and leave that many more copies of plaintext in memory. The values are copied into strings before kiln's `cleanup()` wipes the buffers they point at.

## What the tests prove

Most of the suite runs against a `Decrypter` fake, so the decisions this package makes — prefix stripping, the miss-versus-error split, `Optional()`, read-once, key listing — are covered without any key material.

Faking alone would not be enough, though. A test that fakes the decryption and then asserts the values come back is asserting the fake, and the module's whole claim is that pointing it at a `kiln.toml` yields the decrypted values. So a second file does the real thing end to end: it generates an age identity, writes a `kiln.toml`, encrypts a fixture **with kiln itself**, and reads it back through `cfgkit.Load`.

That real path also proves the safety claim rather than asserting it. One test encrypts for one identity and reads with a different one, and pins that the result is kiln's own `security error: cannot decrypt` — not an empty value a deploy would happily proceed on. It checks the error *shape*, because a test that only checked `err != nil` would still pass if the fixture were broken, and a security test that passes on a broken fixture is worth nothing.

Auto-discovery is covered too, which matters because it is the path a developer who simply runs the program takes. The test redirects `HOME` to a temp directory and plants a key it generated, so it exercises kiln's real discovery logic without ever reading the identity on the machine running the suite.

No test reads the developer's real `~/.kiln`, and no key material is committed — every identity is generated into a temp directory and discarded.

## Options

| Option | Effect |
|---|---|
| `WithKeyPath(path)` | Name the private key instead of discovering one |
| `WithPrefix(prefix)` | Keep only prefixed variables, and strip the prefix |
| `WithDecrypter(d)` | Replace the decryption step — testing, or a shared identity |
| `Optional()` | An empty environment is an empty source, not an error |

## Interfaces

- `cfgkit.Source` — the flat lookup.
- `cfgkit.KeyLister` — a variable in the file that matches no field is reported through `Result.Unknown()` as a probable typo. The file was written for this application, so it can honestly enumerate. Listed names are the stripped ones, or the warning would point at a spelling no struct can bind.
