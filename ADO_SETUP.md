# ADO Setup

## Overview

Before the DALEC Spec Generation pipeline can read source code from your Azure DevOps repository, the service principal must have **Reader** access to your ADO project.

## Steps

### 1. Check if the service principal already has access

1. Navigate to your ADO project: `https://dev.azure.com/msazure/<YourProject>`
2. Go to **Project Settings** → **Permissions** → **Readers**.
3. In the **Members** tab, search for the AAD app **`AKS-Managed-Dalec-ADO-Read-Access`**.
4. If it is listed, you are done — no further action is needed.

### 2. Add the service principal as a Reader

If `AKS-Managed-Dalec-ADO-Read-Access` is **not** listed:

1. In the **Readers** group Members tab, click **Add**.
2. Search for **`AKS-Managed-Dalec-ADO-Read-Access`**.
3. Select the AAD app and click **Save**.

This grants the pipeline read-only access to your project's repositories so it can fetch Dockerfiles, Makefiles, and other build files during spec generation.
