# Contributing to cfgkit

Thanks for your interest in improving **cfgkit**. This guide covers how to get set up, the invariants that must not break, and what a good pull request looks like.

By participating, you agree to abide by our [Code of Conduct](CODE_OF_CONDUCT.md).

## Getting started

```sh
git clone https://github.com/ubgo/cfgkit.git
cd cfgkit
task test      # every module: the core and each contrib adapter
task ci        # the full gate: fmt-check + vet + race tests, everywhere
task cover     # statement coverage per module — the numbers the README quotes
```

The repository is several Go modules: the core at the root (which also holds `cfgkittest`, the shared conformance harness, and `mock/`, the cross-format fixture set), one module per adapter under `contrib/`, and `examples/`. A `go.work` at the root stitches them together, so a change to the core is visible to every adapter immediately. `examples/` is a separate module on purpose — an example importing `contrib/format-yaml` would otherwise put that adapter in the dependency graph of every program that imports cfgkit. Everything runs through the [Taskfile](Taskfile.yml); `task --list` shows the rest.

## The invariants (non-negotiable)

Every one of these is pinned by a test. A change that trips one is wrong, not the test.

- **Zero input works.** `Load` with no sources and an empty environment returns a valid config and a nil error when `Defaults` is complete. This is the property everything else hangs off.
- **Precedence is positional.** The value of a field equals the last source that claimed its key. Never re-ordered internally, never special-cased.
- **`Load` never writes to `os.Environ`,** with exactly one opt-in exception: a field tagged `unset` removes its own key.
- **A flag default never wins.** Only flags the user actually typed count — `fs.Visit`, never `fs.VisitAll`.
- **Secrets never appear in output.** A `secret:"true"` value is absent from `Explain`, `JSON`, and error text unless `Reveal()` is passed.
- **All errors, not the first.** N independent problems report N errors via `errors.Join`.

## The dependency rule

**The core module has one dependency: `github.com/ubgo/dotenv`.** Anything that carries another goes in its own module under `contrib/`, so a consumer compiles only what it imports. `task deps` checks this.

If your change adds an import to the root module, it is almost certainly a `contrib/` module instead.

## Writing an adapter

Copy `contrib/format-yaml` — it is the smallest complete example. Each adapter module carries its own `go.mod`, `README.md`, `LICENSE`, `Taskfile.yml`, and tests, and pins the root module.

Two rules specific to adapters:

- **Test against an interface with a fake, never a live service.** An adapter whose tests need a running Vault will not be run, and an untested adapter is worse than none because it looks supported. `cfgkittest` provides the fake.
- **Run the shared conformance suite.** `cfgkittest.RunSourceTests` and `RunStructuredTests` assert every guarantee the built-in sources make, so twenty adapters behave identically without twenty authors each remembering the rules.

## Examples are documentation, and they are tested

Every code sample in the README comes from `example_test.go`, whose `// Output:` blocks are verified by `go test`. If you change output, the example fails and both get fixed together. Please do not paste output into a document by hand.

## Pull request checklist

- [ ] The change is scoped and described (link the issue it closes).
- [ ] `task ci` passes across every module.
- [ ] New behaviour has a pinning test whose comment names the arm it covers.
- [ ] The root module still has exactly one dependency (`task deps`).
- [ ] Doc comments explain **why** — the policy and the invariant — not what the code does.
- [ ] README/CHANGELOG updated for user-facing changes.
- [ ] No unrelated files or formatting churn.

## Changelog

User-facing changes go under `[Unreleased]` in [CHANGELOG.md](CHANGELOG.md), following [Keep a Changelog](https://keepachangelog.com/).

## Questions

Open a [discussion or issue](https://github.com/ubgo/cfgkit/issues). We're happy to help you land your first contribution.
