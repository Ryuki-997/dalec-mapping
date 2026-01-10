# Dalec Spec Generator - Command Reference

## Overview

The Dalec Spec Generator is a command-line tool for creating Dalec specification files from GitHub repositories. It uses a single-command architecture with direct field flags for simple and powerful spec generation.

## Command

### Usage

```bash
dalec -repo owner/repo [options]
```

The only required flag is `-repo`. All other options are optional and allow you to customize the generated spec.

---

## Options

### Required

| Flag     | Type   | Description                                                                |
|----------|--------|----------------------------------------------------------------------------|
| `-repo`  | string | GitHub repository (owner/repo, full URL, or URL with branch/subdirectory)  |

### Optional Paths

| Flag | Type | Default | Description |
| ---- | ---- | ------- | ----------- |
| `-dockerfile` | string | - | Path to Dockerfile for parsing build instructions |
| `-spec` | string | `output.yml` | Path to previous spec file (preserves revision numbers) |
| `-output` | string | `output.yml` | Output file path for generated spec |

### Field Overrides

Override specific fields in the generated spec:

| Flag | Type | Description |
| ---- | ---- | ----------- |
| `-name` | string | Package name |
| `-description` | string | Package description |
| `-license` | string | License identifier (e.g., MIT, Apache-2.0) |
| `-tag` | string | Specific git tag to use (fetches commit SHA for that tag) |

### Build Configuration

| Flag      | Type   | Description                                     |
|-----------|--------|-------------------------------------------------|
| `-target` | string | Build target (can be specified multiple times)  |

Available build targets:

- `azlinux3/rpm` - Azure Linux 3 RPM package
- `azlinux3/container` - Azure Linux 3 container
- `azlinux3/deb` - Azure Linux 3 DEB package
- `noble/deb` - Ubuntu Noble (24.04) DEB package
- `noble/container` - Ubuntu Noble (24.04) container

### Control Flags

| Flag     | Type | Description                                                  |
|----------|------|--------------------------------------------------------------|
| `-v`     | bool | Enable verbose output                                        |
| `-h`     | bool | Show contextual help (only flags used in current command)    |
| `-help`  | bool | Show full help with all available flags                      |

---

## Examples

### Basic Usage

```bash
# Generate spec from repository (minimal)
dalec -repo microsoft/azure-cns

# Generate with verbose output
dalec -repo owner/repo -v

# Use specific branch
dalec -repo https://github.com/owner/repo/tree/develop

```

### With Field Overrides

```bash
# Override package name
dalec -repo owner/repo -name myapp

# Override multiple fields
dalec -repo owner/repo -name myapp -version v2.0.0 -license MIT

# Use specific git tag
dalec -repo owner/repo -tag v1.5.0
```

### With Dockerfile Parsing & Custom Output Paths

```bash
# Parse Dockerfile for build instructions
dalec -repo owner/repo -dockerfile ./Dockerfile

# Specify custom output file
dalec -repo owner/repo -output custom-spec.yml

# Preserve previous spec and create new output
dalec -repo owner/repo -spec old-spec.yml -output new-spec.yml
```

### With Build Targets

```bash
# Specify single target
dalec -repo owner/repo -target azlinux3/rpm

# Specify multiple targets
dalec -repo owner/repo \
  -target azlinux3/rpm \
  -target azlinux3/container \
  -target noble/deb
```

### Getting Help

```bash
# Show full help documentation
dalec -help

# Show contextual help (only flags you're using)
dalec -repo owner/repo -name myapp -h
```

---

## Features

### Automatic GitHub Integration

- Fetches repository metadata from GitHub API
- Retrieves latest commit SHA, description, and license
- Supports fetching specific git tags with `-tag` flag
- Handles repository URLs with branches and subdirectories

### Dockerfile Parsing

- Parses Dockerfile using Docker Buildkit frontend
- Extracts build instructions and environment variables
- Integrates build steps into generated spec

### Revision Management

- Preserves revision numbers from previous spec files
- Auto-increments revision when version changes
- Resets revision to 1 when version is updated

### Contextual Help

The `-h` flag shows only the flags you're currently using:

```bash
$ dalec -repo owner/repo -name myapp -license MIT -v -h

Usage: dalec -repo owner/repo [options]

Currently used flags:

  -repo owner/repo
      GitHub repository (owner/repo or URL)

  -name myapp
      Override package name

  -license MIT
      Override license identifier

  -v
      Enable verbose output

Run 'dalec -help' for full documentation
```

---

## Workflow Examples

### Basic Workflow

```bash
# 1. Generate initial spec
dalec -repo owner/repo

# 2. Review the generated output.yml
cat output.yml

# 3. Regenerate with custom fields
dalec -repo owner/repo -name myapp -license Apache-2.0

# 4. Generate for specific tag
dalec -repo owner/repo -tag v1.5.0
```

## Additional Resources

- [Dalec Project](https://github.com/project-dalec/dalec) - Official Dalec documentation
- [Dalec Spec Format](https://github.com/project-dalec/dalec/blob/main/docs/spec.md) - Specification reference
- [GitHub API](https://docs.github.com/en/rest) - GitHub REST API documentation
- [YAML Specification](https://yaml.org/spec/) - YAML format reference
- [Docker Buildkit](https://docs.docker.com/build/buildkit/) - Dockerfile parser backend
