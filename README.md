# Dalec Spec Generator

Converts Dockerfiles to Dalec specifications with automatic GitHub metadata integration.

## Features

✅ **Parse Dockerfiles** using Docker Buildkit frontend
✅ **Fetch GitHub metadata** automatically (commit, description, license)  
✅ **Generate Dalec specs** with proper formatting
✅ **Flexible map-based IR** - solves all nested structure issues
✅ **CLI interface** for easy integration

## Installation

```bash
go build -o dalec-gen main.go
```

## Usage

### Basic Usage

```bash
./dalec-gen -repo Ryuki-997/HelloWorld
```

### All Options

```bash
./dalec-gen [options]

Options:
  -repo string
        GitHub repository (required)
        Examples: owner/repo, https://github.com/owner/repo
        
  -dockerfile string
        Path to Dockerfile (default: "Dockerfile")
        
  -output string
        Output YAML file path (default: "test.yml")
        
  -v    Verbose output (shows detailed parsing info)
```

### Examples

```bash
# Basic conversion
./dalec-gen -repo microsoft/azure-cns

# Custom Dockerfile and output
./dalec-gen -repo owner/repo -dockerfile ./custom.Dockerfile -output spec.yml

# Verbose mode
./dalec-gen -repo owner/repo -v

# Using full GitHub URL
./dalec-gen -repo https://github.com/owner/repo
```

## What Gets Auto-Filled

When you provide a GitHub repository, the tool automatically fetches:

- ✅ **Git URL**: Source repository URL
- ✅ **Commit**: Latest commit SHA from default branch  
- ✅ **Website**: Repository homepage (or GitHub URL)
- ✅ **Description**: Repository description
- ✅ **License**: SPDX license identifier
- ✅ **Package name**: Derived from repository name

## Output

The tool generates a complete Dalec spec YAML file with:

- Build args (VERSION, COMMIT, etc.)
- Source definitions with Git URLs
- Dependencies (build and runtime)
- Build steps from Dockerfile RUN commands
- Artifacts (binaries, licenses)
- Image configuration (entrypoint, symlinks)
- Target-specific configs

## Example Output

```yaml
# syntax=ghcr.io/azure/dalec/frontend:latest

args:
  COMMIT: 84da35fdaa6b73a8e48b11ca962378323052c2bb
  VERSION: "0.1"
  
name: helloworld

sources:
  HelloWorld:
    git:
      url: https://github.com/Ryuki-997/HelloWorld
      commit: ${COMMIT}
    generate:
      - gomod: {}
      
dependencies:
  build:
    msft-golang: {}
    
build:
  env:
    CGO_ENABLED: "1"
    GOEXPERIMENT: systemcrypto
  steps:
    - command: |
        go build -o bin/binary ./main.go
        
# ... more sections
```

## Architecture

### Components

1. **Parser** (`parser/`) - Uses Docker Buildkit to parse Dockerfiles
2. **GitHub Client** (`github/`) - Fetches repository metadata from GitHub API
3. **Transformer** (`transformer/`) - Converts parsed data to Dalec spec format
4. **Writer** (`transformer/writer.go`) - Serializes to formatted YAML

### Key Design

Uses `map[string]interface{}` for flexible IR:
- **Dynamic keys**: Handle repository names, dependency names as keys
- **Easy nesting**: Path-based helpers like `Set("build.env.VERSION", value)`
- **No rigid structs**: Add fields dynamically without code changes
- **Auto-formatting**: YAML library handles all indentation

See [ARCHITECTURE.md](ARCHITECTURE.md) for detailed design information.

## Development

### Project Structure

```
dalec-mapping/
├── main.go                 # CLI entry point
├── parser/
│   └── parser.go          # Dockerfile parser (uses buildkit)
├── github/
│   ├── client.go          # GitHub API client
│   └── helpers.go         # Helper functions
├── transformer/
│   ├── transformer.go     # Dockerfile → Dalec converter
│   └── writer.go          # YAML serialization
├── Dockerfile             # Example input
├── tmp.yml                # Reference Dalec spec
└── test.yml               # Generated output
```

### Running Tests

```bash
# Test with example repo
go run main.go -repo Ryuki-997/HelloWorld

# Test with custom Dockerfile
go run main.go -repo owner/repo -dockerfile ./path/to/Dockerfile
```

## Limitations

- Requires GitHub repository for full metadata
- Some complex Dockerfile features may need manual adjustments
- ARG substitutions in Dockerfile are not evaluated
- Multi-stage builds are simplified to primary builder stage

## Manual Fields

Some fields still require manual input:
- Custom build arguments specific to your project
- Additional dependencies not detectable from Dockerfile
- Custom test configurations
- License (if not in GitHub metadata)
- Description (if not in GitHub metadata)

## GitHub API Rate Limiting

The tool uses unauthenticated GitHub API requests:
- Rate limit: 60 requests/hour per IP
- For higher limits, set `GITHUB_TOKEN` environment variable (future enhancement)

## Contributing

Improvements welcome! Key areas:
- Support for more package managers (npm, pip, etc.)
- Better dependency detection
- Support for GitLab, Bitbucket, etc.
- Authentication for higher API limits

## License

MIT
