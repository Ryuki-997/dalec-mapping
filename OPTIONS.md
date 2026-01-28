# User Interaction Options

## Generator AI Workflow

The spec generation is powered by an AI agent. See the full skill definition: [SKILL.md](https://github.com/Ryuki-997/dalec-mapping/blob/main/generator/skills/dalec-spec-generator/SKILL.md)

### Agent Process

1. **Deterministic Discovery**: Agent scans the repository to locate all Dockerfiles and Makefiles
2. **Non-Deterministic Extraction**: AI analyzes files to extract build steps, dependencies, and configuration values
3. **Deterministic Output**: Agent populates the spec file template with extracted values

> **Note**: This workflow has been tested across general cases with some edge cases documented in [issues.md](https://github.com/Ryuki-997/dalec-mapping/blob/main/generator/issues.md)

---

## Goal

This document outlines how users can generate a DALEC spec file from their repository. The goal is to provide flexible interaction models that cater to different user preferences:

- **Simplicity**: Users who want a quick, no-setup solution
- **Privacy**: Users who prefer all processing to stay local
- **Automation**: Users who want spec generation integrated into their CI/CD workflow

### Post-Generation Customization

After generating a spec file, users can further modify it:

- **Direct editing**: Understand and edit the spec file manually using the [template reference](https://github.com/Azure/dalec-build-defs/blob/main/template.yml)
- **CLI tools**: Use helper commands documented in [COMMANDS.md](https://github.com/Ryuki-997/dalec-mapping/blob/main/COMMANDS.md)

---

## User Requirements

### Required Input

| Format | Example |
| ------ | ------- |
| `owner/repo` | `microsoft/dalec` |
| `owner/repo/branch` | `microsoft/dalec/main` |
| `owner/repo/branch/subdir` | `microsoft/dalec/main/packages/core` |

- Repository must be **publicly accessible** for now

### Recommended Files

- **Dockerfiles**: If present anywhere in the repo/branch, they will be deterministically fetched by the agent
- **Makefiles**: If present anywhere in the repo/branch, they will be deterministically fetched by the agent

---

## Option 1: Cloud-Based CLI Tool

A command-line tool that sends a POST request to an Azure service. Once accepted, a cloud agent invokes a serverless function to generate the spec file and returns it to the user.

### 1.1 Workflow

1. User runs CLI command with repo details
2. CLI sends POST request to Azure service
3. Azure triggers serverless function (cloud agent)
4. Spec file is generated server-side
5. Result is returned to the user

### 1.2 Output

- Spec file is stored in Azure Blob Storage
- User receives a **downloadable URL** with self-destruct (time-limited access)

### 1.3 Pros

- **Consistent environment**: All processing happens in a controlled cloud environment
- **No local setup required**: Users don't need to install dependencies or clone repos
- **Scalable**: Serverless architecture handles variable load automatically
- **Centralized updates**: Generator improvements are instantly available to all users
- **Auditable**: All requests can be logged and monitored

### 1.4 Cons

- **Network dependency**: Requires internet connectivity
- **Latency**: Round-trip to cloud adds processing time
- **Cost**: Azure compute and function invocation costs
- **Privacy concerns**: User code/repo details are sent to external service
- **Maintenance burden**: Requires ongoing cloud infrastructure management

---

## Option 2: Local Copilot-Driven Generation

Users clone the generator repository and use GitHub Copilot to run `skill.md` against their target repo locally.

### 2.1 Workflow

1. User clones the generator repo
2. User opens their target repo in VS Code
3. User prompts Copilot: "run skill.md with repo"
4. Copilot generates the spec file locally

### 2.2 Output

- Spec file is **generated locally** in the user's workspace

### 2.3 Pros

- **No cloud infrastructure**: Zero hosting/maintenance costs
- **Privacy-first**: All processing stays local, no code leaves the machine
- **Offline capable**: Works without internet (after initial clone)
- **User control**: Users can customize or extend the skill as needed
- **No latency**: Direct local execution

### 2.4 Cons

- **Copilot dependency**: Requires users to have GitHub Copilot access
- **Setup overhead**: Users must clone repo and understand the workflow
- **Version fragmentation**: Users may run outdated versions of the skill
- **Support complexity**: Harder to debug issues across different environments

---

## Option 3: Build Pipeline Integration

Spec file generation runs as a step in the CI/CD pipeline (GitHub Actions, Azure DevOps, etc.), triggered on commits or PRs.

### 3.1 Workflow

1. User adds a pipeline step to their repo (e.g., GitHub Action or Azure DevOps task)
2. On push/PR, pipeline checks out the repo
3. Pipeline runs the generator as a build step
4. Spec file is generated and committed back or published as an artifact
5. Optionally, PR is auto-created with the updated spec

### 3.2 Output

- Pipeline creates a **Pull Request** with the generated spec file onto the target repository

### 3.3 Pros

- **Automated**: No manual intervention required after initial setup
- **Always up-to-date**: Spec regenerates on every code change
- **Version controlled**: Generated spec is committed alongside code
- **CI/CD native**: Fits naturally into existing DevOps workflows
- **Reproducible**: Runs in a consistent, containerized environment

### 3.4 Cons

- **Pipeline dependency**: Requires CI/CD infrastructure (GitHub Actions, ADO, etc.)
- **Build time overhead**: Adds time to pipeline execution
- **Initial setup**: Users must configure the pipeline step
- **Debugging complexity**: Failures happen in CI, not locally
- **Permissions**: Pipeline needs write access to commit spec files or create PRs

---

## Comparison Summary

| Aspect           | Option 1 (Cloud CLI)       | Option 2 (Copilot)  | Option 3 (Pipeline)    |
| ---------------- | -------------------------- | ------------------- | ---------------------- |
| Setup Complexity | Low                        | Medium              | Medium                 |
| Privacy          | ⚠️ Cloud                   | ✅ Local            | ✅ Repo-scoped         |
| Cost             | 💰 Azure costs             | Free                | Free (with CI mins)    |
| Offline Support  | ❌ No                      | ✅ Yes              | ❌ No                  |
| Maintenance      | High                       | Low                 | Low                    |
| Automation       | ❌ Manual trigger          | ❌ Manual trigger   | ✅ Fully automated     |
| Consistency      | ✅ High                    | ⚠️ Variable         | ✅ High                |
| **Output**       | 🔗 Self-destruct URL       | 📁 Local file       | 🔀 Pull Request        |
