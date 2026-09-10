# Changelog

Kept in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) form, following [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This module versions **independently of the core**, which is the point of it being a separate module: a fix here does not force a release of `cfgkit`, and a release of `cfgkit` does not force one here.

## [Unreleased]

Nothing released yet. The module is complete and covered, and waits on the core's first tag — it pins `github.com/ubgo/cfgkit`, and a published module cannot depend on an unpublished one.

### Added

- `ConfigMap(name, opts...)` and `Secret(name, opts...)` — a named Kubernetes resource as a flat cfgkit source. Secret values are base64-decoded, so both kinds behave identically to a caller.
- Implements `cfgkit.KeyLister`, so a key in the resource that matches no field is reported through `Result.Unknown()` as a probable typo. A ConfigMap's keys were written for this application, which is why it can honestly enumerate.
- Options: `WithNamespace`, `WithServer`, `WithToken`, `WithClient`, `WithServiceAccountDir`, `WithContext`, `Optional`.

### Notes

- **No dependencies.** `net/http` and `encoding/json`, against the API kubelet already gives every pod a credential for. `client-go` is a very large dependency for a single GET, and it ties the module to the cluster's release cadence.
- **A missing resource is an error**, unlike `FromFiles`. You named the resource, so its absence is a manifest mistake rather than a normal outcome; `Optional()` opts out.
- **Read once**, at construction. `Lookup` runs per bound field, so a per-key read would turn a fifty-field configuration into fifty round-trips at boot.
- Not supported, deliberately: kubeconfig, `exec`/OIDC credential plugins, watching, listing, `binaryData`. Each has a one-line answer in the README, and `cfgkit.SourceFunc` wraps `client-go` in five lines when a program already has it.
