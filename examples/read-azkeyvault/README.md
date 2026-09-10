# read-azkeyvault — Azure Key Vault

```sh
go run ./read-azkeyvault
```

**Runs with no Azure subscription.** The example stands up a fake vault *and* a fake IMDS endpoint.

## No dependencies

Same reasoning as [GCP](../read-gcpsecrets): managed identity returns a finished bearer token from one URL, so the platform's own mechanism is enough and no SDK is needed.

## Real usage

```go
az.Secret("checkout-db-password", "PASSWORD", az.WithVault("acme-prod"))
```

A **bare vault name** is expanded to `https://acme-prod.vault.azure.net`, because that is how everyone refers to a vault in practice. A full URL is used as given.

## What it prints

```
host=localhost password-loaded=true

FIELD     KEY       VALUE      SOURCE
Host      HOST      localhost  default
Password  PASSWORD  ••••••     azurekeyvault:checkout-db-password
```

## The IMDS exchange is verified, not assumed

The fake IMDS **rejects any request without `Metadata: true`** — the header that stops a browser being tricked into fetching a token. A load that succeeds therefore proves the header was sent.

## Errors name the fix for three confusing failures

Azure's failures are unusually hard to read, so the adapter translates them:

- **403** names *both* permission models — RBAC role assignments and vault access policies. A vault can be governed by either, and checking the wrong one wastes an afternoon.
- **401** names the **audience**. A wrong-audience token is syntactically valid and looks like a working credential, so the generic "unauthorized" is actively misleading.
- **400 from IMDS** names `WithClientID`. It means the VM has several managed identities and Azure will not pick one for you.

Writing this adapter's tests caught one making a **live call to Azure**; it now uses a fake transport. A test that reaches the internet is a test that passes for the wrong reason on the author's machine and fails in CI.

## Next

- [`read-gcpsecrets`](../read-gcpsecrets) — the same shape on GCP
- [`read-k8s`](../read-k8s) — the same "credential is already here" idea in a cluster
