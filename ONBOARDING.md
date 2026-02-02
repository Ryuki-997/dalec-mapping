# Partner Onboarding Guide

## Overview

This guide explains how partner teams can onboard their repositories to the DALEC Spec Generation service.

## Prerequisites

- A GitHub repository containing the source code you want to generate DALEC specs for
- Access to create pull requests on [azure-management-and-platforms/aks-dalec-build-defs](https://github.com/azure-management-and-platforms/aks-dalec-build-defs)

## Onboarding Steps

### Step 1: Create Your Configuration File

Create a file named `default.yml` in your team's directory with the following structure:

```yaml
# Required: Repository reference
repository: owner/repo                # Basic format (preferred)
# OR
repository: owner/repo/branch         # With specific branch
# OR 
repository: owner/repo/branch/subdir  # Wtih Sub-directory

# Optional: Dockerfile paths (list)
dockerfiles:
  - path/to/Dockerfile
  - another/path/to/Dockerfile

# Optional: Makefile paths (list)
makefiles:
  - Makefile
  - build/Makefile
```

### Step 2: Submit Pull Request

1. Fork or clone [azure-management-and-platforms/aks-dalec-build-defs](https://github.com/azure-management-and-platforms/aks-dalec-build-defs)

2. Add your `default.yml` file:

   ```bash
   definitions/
   └── your-team-name/
       └── project1/
           └── default.yml
       └── project2/
           └── default.yml
   ```

3. Create a pull request targeting the repository

4. The PR will be reviewed and manually merged to the dedicated private branch

### Step 3: Confirmation

Once your PR is merged, your repository is onboarded and ready for DALEC spec generation.

## Example Configuration

### Minimal Configuration

```yaml
# definitions/my-team/default.yml
repository: myorg/myrepo
```

### Full Configuration

```yaml
# definitions/my-team/default.yml
repository: myorg/myrepo/main

dockerfiles:
  - Dockerfile
  - docker/Dockerfile.alpine
  - docker/Dockerfile.ubuntu

makefiles:
  - Makefile
  - src/Makefile
```
