# ADO Setup

## Overview

Before the DALEC Spec Generation pipeline can read source code from your Azure DevOps repository, the service principal must have read access to your ADO project.

Authentication uses **Workload Identity Federation** (OIDC). On AKS the workload identity webhook injects federated credentials into the pod automatically. The pipeline acquires a short-lived Entra ID access token scoped to Azure DevOps — no long-lived PATs are stored or rotated.

## Steps

### 1. Navigate to your ADO project

Go to `https://dev.azure.com/<org>/<project>`.

### 2. Add the service principal to a permission group

1. Go to **Project Settings** → **Permissions**.
2. Select a permission group that grants **View project-level information** (e.g., **Readers**).
3. In the **Members** tab, click **Add**.
4. Search for **`AKS-Managed-Dalec-ADO-Read-Access`** and add it.

### 3. Verify repository-level access

1. Navigate to **Project Settings** → **Repositories** → select your repository.
2. Go to the **Security** tab.
3. Search for **`AKS-Managed-Dalec-ADO-Read-Access`**.
4. Confirm the service principal is listed and has **Read** access.

## Troubleshooting

If you have trouble adding the service principal in Project Settings, consider reaching out to the **OneBranch Bot** for assistance: https://eng.ms/docs/products/onebranch/helpandsupport/onebranchbot

### Service Principal Details

| Field | Value |
|-------|-------|
| Application (client) ID | `880e12fe-f5bc-48e7-a96c-826f0e6dd75b` |
| Object ID | `d847cc06-42ed-4f63-981d-6478ee806876` |
| Directory (tenant) ID | `72f988bf-86f1-41af-91ab-2d7cd011db47` |
