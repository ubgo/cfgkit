# read-kiln — encrypted secrets you can commit

```sh
go run ./read-kiln
```

**Runs with no setup.** The example generates an age identity, writes a `kiln.toml`, and encrypts a fixture **with kiln itself** — all in a temp directory, all discarded on exit. Nothing touches `~/.kiln`.

## Why this one is different

Every other secret source in this catalogue answers the same question — *where do I put the secrets so they are not in the repository?* — and every answer costs the same thing: the configuration now lives in two places, and a developer cloning the repository gets half of it.

kiln answers differently. The encrypted file **is committed**, next to the code, and only the age or SSH identities named in `kiln.toml` can read it. One place to look, one thing to clone, and a secret rotation shows up as a normal diff with normal review.

That is a genuinely different trade, not a different vendor — which is the bar for adding a module here.

## Real usage

```go
kiln.Env("kiln.toml", "production")
```

The key is **discovered** the way the kiln CLI discovers it (`~/.kiln/kiln.key`, `~/.ssh/id_ed25519`), so a developer who can already run `kiln` needs nothing more. CI usually cannot rely on that, so `WithKeyPath` names it.

## What it prints

```
kiln.internal:8600

FIELD     KEY       VALUE          SOURCE
Host      HOST      kiln.internal  kiln:production
Password  PASSWORD  ••••••         kiln:production
Port      PORT      8600           kiln:production

with an identity the file does not grant:
  cannot decrypt 'production' (ensure your key has access to this file)
```

## Denial is an error, never a miss

That last line is the safety argument, **executed rather than asserted**. The example generates a second identity that `kiln.toml` does not grant, attempts a read, and shows kiln's own refusal.

A source that reported "not found" when it meant "not allowed" would let a deploy proceed with an empty database password, and nothing downstream could tell the difference. The test checks the error's *shape*, not just that one occurred — a test that accepted any error would still pass if the fixture were broken, and a security test that passes on a broken fixture is worth nothing.

## It takes a dependency, on purpose

There is no transport here. The hard part is age decryption and the access-control model, and a hand-rolled version of either would be a worse copy of something that already exists — with a failure mode, in the cryptography case, that does not announce itself.

## Prefixes strip

`WithPrefix("API_")` keeps prefixed variables and **removes** the prefix, so one encrypted file can serve several components without every struct repeating the namespace. It is a rename, not an alias: afterwards `PORT` resolves and `API_PORT` does not.

## Next

- [`read-secretsmanager`](../read-secretsmanager) — the "move it elsewhere" answer
- [`read-file`](../read-file) — the same file, unencrypted
