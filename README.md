# Dalec Spec Generator

Generate Dalec specifications from GitHub repositories.

## Installation

```bash
go build -o spec main.go
```

## Usage

### Public Repository

```bash
./spec generate -repo owner/repo
```

### Private Repository

```bash
./spec generate -repo owner/repo -auth YOUR_GITHUB_TOKEN
```

This generates a `output.yml` spec file ready to use.

## Examples

```bash
# Generate spec from public repo
./spec generate -repo microsoft/azure-cns

# Generate spec from private repo
./spec generate -repo myorg/private-repo -auth ghp_xxxxxxxxxxxx

# Custom output file
./spec generate -repo owner/repo -output myspec.yml
```

## Customizing Your Spec

After generation, you may need to modify the spec file. Use the built-in commands:

```bash
# Get a value
./spec get -field version

# Set a value
./spec set -field version -value v2.0.0
./spec set -field license -value MIT
```

For detailed command options, see [COMMANDS.md](COMMANDS.md).

For the full spec template and all available fields, refer to the [Dalec template](https://github.com/Azure/dalec-build-defs/blob/main/template.yml).

## What's Auto-Generated

The tool fetches from GitHub:

- Git URL and latest commit SHA
- Repository description
- License (SPDX identifier)
- Package name (from repo name)

## License

Microsoft
