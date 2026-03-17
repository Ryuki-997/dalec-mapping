# ADO Setup for OneBranch Pipeline

## Why OneBranch?

The DALEC spec generation service needs to read tags and metadata from partner repositories. For public GitHub repos this works via the GitHub API, but internal ADO repos are not publicly accessible. OneBranch runs the pipeline inside a Microsoft-managed build agent that has implicit access to internal ADO repos — no PATs, no shared credentials, no environment variables. The pipeline simply executes `git ls-remote` against the ADO repo URL and the agent's identity handles authentication automatically.

Partner teams need to complete the steps below so that the OneBranch agent identity (`OneBranch.Platform.Installer`) is granted access to their ADO organization. Without this, the pipeline cannot reach internal repos to fetch tags and commits.

## Step 1: Join Your ADO Organization to Microsoft Enterprise

1. Go to **Organization settings** → **Azure Active Directory** (`https://<your-account>.visualstudio.com/_settings/organizationAad`).
2. Confirm you are joined to the **Microsoft** directory.
3. Under **Policies** (`https://<your-account>.visualstudio.com/_settings/organizationPolicy`), ensure **"Enterprise access to projects"** is **on**.

## Step 2: Create OneBranch Admin Service Account Group

1. Go to **Organization settings** → **Security** (`https://<your-account>.visualstudio.com/_settings/security`).
2. Select **New Group**.
3. Enter group name: `OneBranch Admin Service Account`.
4. Add the service principal **`OneBranch.Platform.Installer`** as a member and click **Create**.
5. Verify the service principal **`OneBranch.Platform.Installer`** appears in the group. If not, try adding it again.

## Step 3: Ensure You Are a Project Collection Administrator

1. Go to **Organization settings** → **Security** (`https://<your-account>.visualstudio.com/_settings/security`).
2. Select the **Project Collection Administrators** group.
3. Navigate to **Members** — you should appear in this list or within a group in this list.
4. If not, ask an existing admin to perform these steps on your behalf or grant you admin access.

## Step 4: Add OneBranch Platform Installer as Project Collection Administrator

1. In the **Project Collection Administrators** group, click **+ Add...**.
2. Add the group **`OneBranch Admin Service Account`** (created in Step 2) as a Project Collection Administrator **and** as a General User.

## Step 5: Onboard Your Repository

Once your ADO organization is set up (Steps 1–4), follow the standard onboarding process in [[ONBOARDING.md](https://github.com/Ryuki-997/dalec-mapping/blob/main/ONBOARDING.md)] to register your repository. The only difference for ADO repos is the `repository` field in `onboard.yml` — use the full ADO URL instead of a GitHub `owner/repo` path:

```yaml
# specs/<your-team>/<your-project>/onboard.yml
repository: https://dev.azure.com/<org>/<project>/_git/<repo>
tags:
  - v1.0.\d+
dockerfile: Dockerfile
makefile: Makefile
```

## Reference 


