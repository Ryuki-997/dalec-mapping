# ADO Setup

## Overview

Before the DALEC Spec Generation pipeline can read source code from your Azure DevOps repository, the service principal must have read access to your ADO project.

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
