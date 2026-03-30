# Partner Onboarding Guide

## Overview

This guide explains how partner teams can onboard their repositories to the DALEC Spec Generation service.

## Prerequisites

- A GitHub repository containing the source code you want to generate DALEC specs for
- Access to create pull requests on [azure-management-and-platforms/aks-dalec-build-defs](https://github.com/azure-management-and-platforms/aks-dalec-build-defs)

## Onboarding Steps

### Step 1: Create Your Configuration File

Create a file named `onboard.yml` under `specs/<team>/<project>/` or `specs/<project>/` with the following structure:

```yaml
# specs/<your-team>/<your-project>/onboard.yml
# or specs/<your-project>/onboard.yml

# Required: GitHub repository (owner/repo)
repository: owner/repo

# Required: One or more tag entries. Each entry is either:
#   - A regex pattern to match against the repository's release tags
#     (e.g. "^v1\\.2\\.\\d+$"). Use anchored regexes for precision.
#   - The special keyword "latest", which always resolves to the
#     most recent release tag in the repository.
tags:
  - "^v1\\.2\\.\\d+$"
  - "^v1\\.3\\.\\d+$"

# Required: Path to the Dockerfile relative to the repository root.
dockerfile: Dockerfile

# Required: Path to the Makefile relative to the repository root.
makefile: Makefile

# Optional: Review mode — controls how the service handles spec generation.
#   ManualReview (default) — When build files (Dockerfile/Makefile) change or
#     on initial generation, the service notifies reviewers and waits for
#     manual approval before promoting the spec.
#   AutoReview — The service automatically generates and promotes specs without
#     waiting for reviewer approval. Trusts the auto-generation process.
reviewMode: ManualReview

# Optional: List of reviewer email addresses for notifications.
# Reviewers are notified when:
#   - A spec is generated for the first time (initial onboard)
#   - Build files (Dockerfile/Makefile) change on a re-onboard (ManualReview mode)
reviewers:
  - alice@example.com
  - bob@example.com
```

### Step 2: Submit Pull Request

1. Fork or clone [azure-management-and-platforms/aks-dalec-build-defs](https://github.com/azure-management-and-platforms/aks-dalec-build-defs)

2. Add your `onboard.yml` file using one of these directory layouts:

   **With team grouping:**

   ```bash
   specs/
   └── your-team-name/
       └── project1/
           └── onboard.yml
       └── project2/
           └── onboard.yml
   ```

   **Without team grouping:**

   ```bash
   specs/
   └── project1/
       └── onboard.yml
   └── project2/
       └── onboard.yml
   ```

3. Create a pull request targeting the repository

4. The PR will be reviewed and manually merged to the dedicated private branch

### Step 3: Confirmation

Once your PR is merged, your repository is onboarded and ready for DALEC spec generation.

## Configuration Reference

| Field            | Required | Description                                      |
| ---------------- | -------- | ------------------------------------------------ |
| `repository`     | Yes      | GitHub repository in `owner/repo` format         |
| `tags`           | Yes      | List of regex patterns or `latest` keyword       |
| `dockerfile`     | Yes      | Path to the Dockerfile relative to the repo root |
| `makefile`       | Yes      | Path to the Makefile relative to the repo root   |
| `reviewMode`     | No       | `ManualReview` (default) or `AutoReview`         |
| `reviewers`      | No       | List of email addresses for notifications        |
| `specImageName`  | No       | Override the generated spec image name           |
| `specRepository` | No       | Override the target spec repository              |

## Example Configurations

### Minimal Configuration

```yaml
# specs/my-project/onboard.yml
repository: myorg/myrepo
tags:
  - latest
dockerfile: Dockerfile
makefile: Makefile
```

### Full Configuration

```yaml
# specs/my-team/my-project/onboard.yml
repository: myorg/myrepo
tags:
  - "^v1\\.2\\.\\d+$"
  - "^v1\\.3\\.\\d+$"
dockerfile: build/Dockerfile
makefile: Makefile
reviewMode: ManualReview
reviewers:
  - alice@example.com
  - bob@example.com
```

### Auto-Review Configuration

```yaml
# specs/my-team/my-project/onboard.yml
repository: myorg/myrepo
tags:
  - "^v\\d+\\.\\d+\\.\\d+$"
dockerfile: Dockerfile
makefile: Makefile
reviewMode: AutoReview
```
