# Partner Onboarding Guide

## Overview

This guide explains how partner teams can onboard their repositories to the DALEC Spec Generation service.

## Prerequisites

- A GitHub repository containing the source code you want to generate DALEC specs for
- Access to create pull requests on [azure-management-and-platforms/aks-dalec-build-defs](https://github.com/azure-management-and-platforms/aks-dalec-build-defs)

## Onboarding Steps

### Step 1: Create Your Configuration File

Create a file named `onboard.yml` under `specs/<team>/<project>/` with the following structure:

```yaml
# specs/<your-team>/<your-project>/onboard.yml

# Required: GitHub repository (owner/repo)
repository: owner/repo

# Required: One or more release tags to generate specs for
tags:
  - v1.2.3
  - v1.3.0

# Optional: Path to the Dockerfile (single)
dockerfile: Dockerfile

# Optional: Path to the Makefile (single)
makefile: Makefile
```

### Step 2: Submit Pull Request

1. Fork or clone [azure-management-and-platforms/aks-dalec-build-defs](https://github.com/azure-management-and-platforms/aks-dalec-build-defs)

2. Add your `onboard.yml` file:

   ```bash
   specs/
   └── your-team-name/
       └── project1/
           └── onboard.yml
       └── project2/
           └── onboard.yml
   ```

3. Create a pull request targeting the repository

4. The PR will be reviewed and manually merged to the dedicated private branch

### Step 3: Confirmation

Once your PR is merged, your repository is onboarded and ready for DALEC spec generation.

## Example Configuration

### Minimal Configuration

```yaml
# specs/my-team/my-project/onboard.yml
repository: myorg/myrepo
tags:
  - v1.0.0
```

### Full Configuration

```yaml
# specs/my-team/my-project/onboard.yml
repository: myorg/myrepo
tags:
  - v1.2.3
  - v1.3.0
dockerfile: Dockerfile
makefile: Makefile
```
