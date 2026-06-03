package ado

// ─── Chunk 2 · GIT CLIENT ───────────────────────────────────────────────────

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/url"
	"os/exec"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// azureDevOpsScope is the well-known resource ID for Azure DevOps,
// used to request an Entra ID access token with ADO read permissions.
const azureDevOpsScope = "499b84ac-1321-427f-aa17-267ca6975798/.default"

var initCredential = sync.OnceValues(func() (*azidentity.DefaultAzureCredential, error) {
	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}
	return credential, nil
})

// cachedADOToken acquires a short-lived Entra ID access token scoped to
// Azure DevOps on the first call and caches it for subsequent calls.
// The token lasts ~1 hour, well beyond a single pipeline run.
// On AKS the WorkloadIdentityCredential is used automatically
// (the webhook injects AZURE_CLIENT_ID, AZURE_TENANT_ID, and
// AZURE_FEDERATED_TOKEN_FILE). For local development AzureCLICredential
// activates via `az login`.
var cachedADOToken = sync.OnceValues(func() (string, error) {
	credential, err := initCredential()
	if err != nil {
		return "", err
	}

	tokenResponse, err := credential.GetToken(context.Background(), policy.TokenRequestOptions{
		Scopes: []string{azureDevOpsScope},
	})
	if err != nil {
		return "", fmt.Errorf("failed to acquire ADO access token: %w", err)
	}

	log.Println("  Acquired Entra ID access token for Azure DevOps")
	return tokenResponse.Token, nil
})

// adoAuthURL injects a short-lived Entra ID access token into the URL as
// HTTP Basic auth so that git subprocesses can authenticate against Azure
// DevOps.  If token acquisition fails the URL is returned unchanged and
// a warning is logged.
func adoAuthURL(rawURL string) string {
	token, err := cachedADOToken()
	if err != nil {
		log.Printf("⚠️  Failed to acquire ADO access token, proceeding without auth: %v", err)
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.User = url.UserPassword("", token)
	return u.String()
}

// gitOut runs git with the given args, optionally inside dir (empty = inherit
// cwd), and returns trimmed stdout.
func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w — %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// gitOutBytes is like gitOut but returns raw bytes.
func gitOutBytes(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w — %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
