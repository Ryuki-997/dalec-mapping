# DALEC Mapping Architecture

## Overview

This document outlines the end-to-end process for generating DALEC spec files from GitHub repositories using a CLI-driven, serverless architecture on Azure.

## High-Level Flow

```bash
┌─────────────────────────────────────────────────────────────────────────────┐
│                             USER WORKFLOW                                   │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  1. User runs CLI locally                                                   │
│     └─► python app.py owner/repo[/branch/subdir]                            │
│                                                                             │
│  2. CLI builds JSON request                                                 │
│     └─► POST /api/agent/process                                             │
│                                                                             │
│  3. Agent processes request                                                 │
│     └─► Executes SKILL.md instructions                                      │
│                                                                             │
│  4. Output returned to user                                                 │
│     └─► Completed spec file or error with guidance                          │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Detailed Process

### Step 1: CLI Invocation

User runs the CLI command locally:

```bash
python app.py owner/repo/branch/subdir
```

The CLI:

- Parses the repository path
- Builds a JSON request payload
- Includes Azure AD token if available (managed identity or user token)
- Calls the Agent HTTP endpoint: `POST /api/agent/process`

### Step 2: Agent Function Authentication

The Azure Function authenticates the caller:

1. **Token Validation**: Validate Azure AD bearer token if present
2. **Identity Mapping**: Map caller to identity and permissions
3. **Allowlist Enforcement**: Check caller and repo against allowlist
4. **Access Denied**: Return `401/403` with instructions if unauthorized

### Step 3: Repository Fetch

Deterministic fetch of repository content:

1. **Credential Retrieval**: Read GitHub credential from Key Vault via Managed Identity
2. **API Request**: Authenticated `GET` to GitHub API for `owner/repo@ref:path`
3. **Caching**: Use `If-None-Match` header if ETag is known
4. **Success (200)**: Decode content and proceed to skill execution
5. **Failure (404/unreachable)**: Trigger auth fallback

### Step 4: Auth Fallback (When Repo Unreachable)

Return structured response to CLI requesting authorization:

#### Option A: Interactive OAuth

```json
{
  "status": "auth_required",
  "method": "oauth",
  "auth_url": "https://auth.example.com/github/authorize?session=abc123",
  "instructions": "Visit the URL to grant read access to the repository"
}
```

- User completes GitHub OAuth flow
- Function receives callback
- Short-lived token stored in Key Vault for session

After authorization, retry fetch and proceed.

### Step 5: Skill Execution

The agent executes the process defined in [`generator/skills/dalec-spec-generator/SKILL.md`](generator/skills/dalec-spec-generator/SKILL.md):

- Skill file loaded as system prompt
- Repository context provided as user input
- Azure OpenAI processes the request
- Step-by-step instructions followed to generate spec

### Step 6: Output Storage

Generated spec file is stored in Azure Blob Storage:

1. **Upload**: Function uploads generated spec to Blob storage
2. **SAS Token**: Short-lived SAS URL generated for user download
3. **Audit**: Metadata logged for tracking

### Step 7: Response

Function responds to CLI:

```json
{
  "status": "success",
  "spec": "<generated DALEC spec content>",
  "artifacts": {
    "spec_url": "https://storage.blob.core.windows.net/specs/abc123.yaml"
  },
  "audit_id": "abc123"
}
```

## Output Handling

### Successful Generation

- Completed spec file returned to user
- Artifacts stored in audit store
- Logs available for review

### Issues Encountered

If the spec file has problems or user wants modifications:

1. **Review COMMANDS.md**: Check available commands for spec manipulation
2. **Direct Modification**: Edit the spec file directly
3. **Re-run Generation**: Invoke CLI again with adjusted parameters

## Security Architecture

```bash
┌─────────────────────────────────────────────────────────────────────────────┐
│                          SECURITY LAYERS                                    │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐                      │
│  │   Azure AD  │───►│  Key Vault  │───►│  GitHub API │                      │
│  │   Auth      │    │  Secrets    │    │  Auth       │                      │
│  └─────────────┘    └─────────────┘    └─────────────┘                      │
│                                                                             │
│  • Bearer token validation          • Managed Identity access               │
│  • Caller allowlist                 • Short-lived SAS tokens                │
│  • Repo allowlist                   • TLS encryption                        │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Components

| Component          | Description                           | Location                                         |
| ------------------ | ------------------------------------- | ------------------------------------------------ |
| CLI Adapter        | Local CLI for user interaction        | `adapter/app.py`                                 |
| Agent Function     | Azure Function for request processing | Azure Functions                                  |
| Skill Definition   | Agent instructions and process        | `generator/skills/dalec-spec-generator/SKILL.md` |
| Commands Reference | Available spec manipulation commands  | `COMMANDS.md`                                    |
| Key Vault          | Secure credential storage             | Azure Key Vault                                  |
| Blob Storage       | Artifact and output storage           | Azure Blob Storage                               |

## Environment Variables

```bash
# Azure OpenAI
AZURE_OPENAI_ENDPOINT=https://your-resource.openai.azure.com/
AZURE_OPENAI_KEY=<your-key>
AZURE_OPENAI_DEPLOYMENT=gpt-4

# GitHub (for private repos)
GITHUB_TOKEN=<pat-or-oauth-token>

# Azure Resources
AZURE_KEYVAULT_URL=https://your-vault.vault.azure.net/
AZURE_STORAGE_ACCOUNT=<storage-account-name>
```
