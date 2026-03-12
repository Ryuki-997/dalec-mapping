# ADO Repository Onboarding Guide

This guide is for partner teams who host their source repository in **Azure DevOps (ADO)** and want to onboard to the DALEC Spec Generation service.

---

## Prerequisites

- Your source repository is hosted in ADO under the `CloudNativeCompute` organization
- You have Project Administrator or Repository Administrator permissions on your ADO project

---

## Step 1: (Org Admin only) Add the Managed Identity to the Organization

This only needs to be done **once** for the entire `CloudNativeCompute` organization.

1. Go to **Organization Settings** (top-left gear icon at `https://dev.azure.com/CloudNativeCompute`)
2. Select **Users** from the left sidebar
3. Click **Add users**
4. Search for and add:
   - **Name:** `spec-generation-ADO-auth`
   - **Client ID:** `8c8fc91c-16f8-4650-a530-f1002846bc04`
5. Assign the **Basic** access level
6. Click **Add**

Once added at the org level, the identity is available to all projects within `CloudNativeCompute` and partner teams can grant it access to their individual repositories without needing org admin rights.

---

## Step 2: (Partner team) Grant Read Access to your Repository

This is the only step required from partner teams.

1. Go to your **Project Settings** (bottom-left gear icon inside your project)
2. Select **Repositories** from the left sidebar
3. Click on your onboarded repository name
4. Select the **Security** tab
5. Under **Users**, find `spec-generation-ADO-auth`
6. Verify that **Read** is set to **Allow**

That's all the access required — the service only needs to read your Dockerfile and Makefile to generate the DALEC spec.

---

## Step 3: Add your `onboard.yml` to the build-defs repository

Create `onboard.yml` under `specs/<your-team>/<your-project>/` in [aks-dalec-build-defs](https://github.com/azure-management-and-platforms/aks-dalec-build-defs), using your ADO repository URL as the `repository` field:

```yaml
# specs/<your-team>/<your-project>/onboard.yml
repository: https://CloudNativeCompute@dev.azure.com/CloudNativeCompute/<project>/_git/<repo>

tags:
  - v1.2.3

dockerfile: Dockerfile
makefile: Makefile
```

See [ONBOARDING.md](ONBOARDING.md) for the full field reference.

---
