# Partner Onboarding Guide

## Overview

This guide explains how partner teams can onboard their repositories to the DALEC Spec Generation service.

## Prerequisites

- A GitHub repository containing the source code you want to generate DALEC specs for
- Access to create pull requests on [azure-management-and-platforms/aks-dalec-build-defs](https://github.com/azure-management-and-platforms/aks-dalec-build-defs)

## Onboarding Steps

### Step 1: Create Your Configuration File

Create a file named `onboard.yml` under `specs/<your-project>/` in the onboard repo.

The top-level keys are component names. The format supports two layouts:

#### Standalone Component

When your project produces a single component, define it as a top-level key:

```yaml
# specs/aks-node-controller/onboard.yml

aks-node-controller:
  repository: https://github.com/Azure/aks-node-controller
  tags:
    - "^v0\\.0\\.\\d+$"
  targets:
    - azlinux3/container
  dockerfile: "."
  makefile: "."
  reviewers:
    - user1
```

#### Multiple Standalone Components

When your project produces multiple independent components:

```yaml
# specs/containernetworking/onboard.yml

azure-cns:
  repository: https://github.com/Azure/azure-container-networking
  tags:
    - "azure-cns/v1\\.6\\..*"
  targets:
    - azlinux3/container
    - windowscross/container
  dockerfile: "."
  makefile: "."
  reviewers:
    - user1

azure-ipam:
  repository: https://github.com/Azure/azure-container-networking
  tags:
    - "azure-ipam/v0\\.4\\..*"
  targets:
    - azlinux3/container
  dockerfile: "."
  makefile: "."
  reviewers:
    - user1
```

#### Grouped Components

When multiple components share the same tag and should be submitted in a single PR, wrap them under a group key:

```yaml
# specs/containernetworking/onboard.yml

containernetworking:
  azure-cns:
    repository: https://github.com/Azure/azure-container-networking
    tags:
      - "^v1\\.6\\.\\d+$"
    targets:
      - azlinux3/container
      - windowscross/container
    dockerfile: "."
    makefile: "."
    reviewers:
      - user1

  azure-ipam:
    repository: https://github.com/Azure/azure-container-networking
    tags:
      - "^v1\\.6\\.\\d+$"
    targets:
      - azlinux3/container
    dockerfile: "."
    makefile: "."
    reviewers:
      - user1
```

The group key (`containernetworking`) becomes the display name in PRs and branch names.

### Step 2: Provide a Test Suite (Optional)

Each component can optionally have its own test suite. The test script receives the built image tag as the first argument and any non-zero exit code fails the pipeline.

**Single component** — place the test script directly under the project:

```text
specs/aks-node-controller/
├── onboard.yml
└── tests/
    └── test.sh
```

**Multiple or grouped components** — create a separate folder for each component and configure distinct test suites:

```text
specs/containernetworking/
├── onboard.yml
├── azure-cns/
│   └── tests/
│       └── test.sh
└── azure-ipam/
    └── tests/
        └── test.sh
```

This ensures each component's tests validate only the binary and behavior relevant to that component.

### Step 3: Submit Pull Request

1. Fork or clone [azure-management-and-platforms/aks-dalec-build-defs](https://github.com/azure-management-and-platforms/aks-dalec-build-defs)

2. Add your `onboard.yml` file (and optional tests):

   ```text
   specs/
   └── your-project/
       ├── onboard.yml
       └── tests/
           └── test.sh
   ```

3. Create a pull request targeting the repository

4. The PR will be reviewed and manually merged to the dedicated private branch

### Step 4: Confirmation

Once your PR is merged, your repository is onboarded and ready for DALEC spec generation.

## Configuration Reference

| Field        | Required | Description                                             |
| ------------ | -------- | ------------------------------------------------------- |
| `repository` | Yes      | GitHub repository URL (`https://github.com/owner/repo`) |
| `tags`       | Yes      | List of regex patterns to match against release tags    |
| `targets`    | Yes      | List of build targets (see below)                       |
| `dockerfile` | Yes      | Path to the Dockerfile relative to the repo root        |
| `makefile`   | Yes      | Path to the Makefile relative to the repo root          |
| `reviewers`  | No       | List of GitHub usernames to request review from         |

## Available Build Targets

| Target                    | Description                            |
| ------------------------- | -------------------------------------- |
| `azlinux3/container`      | Azure Linux 3 container image          |
| `azlinux3/rpm`            | Azure Linux 3 RPM package              |
| `azlinux3/testing/sysext` | Azure Linux 3 testing system extension |
| `noble/deb`               | Ubuntu Noble (24.04) deb package       |
| `jammy/deb`               | Ubuntu Jammy (22.04) deb package       |
| `focal/deb`               | Ubuntu Focal (20.04) deb package       |
| `bionic/deb`              | Ubuntu Bionic (18.04) deb package      |
| `bookworm/deb`            | Debian Bookworm deb package            |
| `windowscross/container`  | Windows cross-compiled container image |
| `windowscross/zip`        | Windows cross-compiled zip archive     |

## Tag Pattern Format

Tags are regex patterns matched against the repository's release tags. **Every tag that matches your pattern will be built as a separate image.** Be careful to use a pattern that satisfies exactly the versions you intend — overly broad patterns will trigger builds for every matching release.

Use anchored patterns for precision:

- `"^v1\\.2\\.\\d+$"` — matches `v1.2.0`, `v1.2.1`, etc.
- `"azure-cns/v1\\.6\\..*"` — matches prefixed tags like `azure-cns/v1.6.0`
- `"^v\\d+\\.\\d+\\.\\d+$"` — matches any semver tag (use with caution — builds every release)
