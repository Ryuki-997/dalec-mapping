# ADO Issues

Known issues when generating and testing DALEC specs for Azure DevOps-sourced repositories.

## 1. ADO Git Auth Header Required for Local Testing

ADO-sourced specs include `auth.header: GIT_AUTH_HEADER` in the git source block. BuildKit needs this secret passed at build time to clone the private repo. Without it, the build fails with:

```
fatal: could not read Username for 'https://dev.azure.com': terminal prompts disabled
```

**Fix:** Pass the secret to `docker build` via `--secret`:

```bash
docker build --secret id=GIT_AUTH_HEADER,env=GIT_AUTH_HEADER ...
```

The `GIT_AUTH_HEADER` env var must contain a valid `Authorization: Bearer <token>` value using an Entra ID access token scoped to Azure DevOps.

## 2. Source Generate Block Uses `gomod: {}` Instead of Inline

The generate block produces `gomod: {}` which may cause subpath resolution issues when the Dockerfile build context is nested (e.g. `./docker`). The subpath field must correctly reflect the Dockerfile directory relative to the repo root.

## 3. Empty License Field in ADO Repos

ADO repositories don't expose license metadata the way GitHub does. The spec generator defaults to an empty or generic license string, which may not match the actual project license.