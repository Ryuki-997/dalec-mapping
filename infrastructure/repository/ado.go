package repository

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// TagInfo holds a tag name and its associated commit SHA.
type TagInfo struct {
	Name   string
	Commit string
}

// fetchUAMIToken acquires an Azure DevOps OAuth token using the given UAMI
// client ID via azidentity. Works on VMs (IMDS), App Service, Container Apps,
// and AKS with Workload Identity.
func fetchUAMIToken(clientID string) (string, error) {
	cred, err := azidentity.NewManagedIdentityCredential(&azidentity.ManagedIdentityCredentialOptions{
		ID: azidentity.ClientID(clientID),
	})

	if err != nil {
		return "", fmt.Errorf("creating managed identity credential: %w", err)
	}

	token, err := cred.GetToken(context.Background(), policy.TokenRequestOptions{
		Scopes: []string{"499b84ac-1321-427f-aa17-267ca6975798/.default"},
	})

	fmt.Println("Acquired token for client ID", token)

	if err != nil {
		return "", fmt.Errorf("acquiring token: %w", err)
	}


	return token.Token, nil
}

// FetchAllADOTags queries a remote ADO repository for all tags using git ls-remote.
// If UAMI_CLIENT_ID is set in the environment, it acquires an OAuth token via
// Azure IMDS and passes it to git. Otherwise it falls back to ambient credentials.
// For annotated tags the dereferenced commit (^{}) is used as the commit SHA.
func FetchAllADOTags(repoURL string) ([]TagInfo, error) {
	clientID := os.Getenv("UAMI_CLIENT_ID")
	if clientID == "" {
		return nil, fmt.Errorf("UAMI_CLIENT_ID is not set")
	}

	token, err := fetchUAMIToken(clientID)
	if err != nil {
		return nil, fmt.Errorf("UAMI token acquisition failed: %w", err)
	}
	cmd := exec.Command("git", "ls-remote", "--tags", repoURL)
	cmd.Env = append(os.Environ(),
		"GIT_ASKPASS=true",
		"GCM_INTERACTIVE=Never",
		"GIT_TERMINAL_PROMPT=0",
		"AZURE_DEVOPS_EXT_PAT="+token,
	)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-remote --tags failed for %s: %w", repoURL, err)
	}

	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}

	// ls-remote output:
	//   <sha>\trefs/tags/v1.0.0           ← lightweight or annotated tag object
	//   <sha>\trefs/tags/v1.0.0^{}        ← dereferenced commit of annotated tag
	// For annotated tags we prefer the ^{} line (actual commit).
	tagCommits := make(map[string]string) // tag name → commit SHA
	var order []string                    // preserve insertion order

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		sha, ref := parts[0], parts[1]

		if strings.HasSuffix(ref, "^{}") {
			// Dereferenced annotated tag — overwrite with the real commit SHA
			name := strings.TrimPrefix(strings.TrimSuffix(ref, "^{}"), "refs/tags/")
			tagCommits[name] = sha
		} else {
			name := strings.TrimPrefix(ref, "refs/tags/")
			if _, exists := tagCommits[name]; !exists {
				tagCommits[name] = sha
				order = append(order, name)
			}
		}
	}

	tags := make([]TagInfo, 0, len(order))
	for _, name := range order {
		tags = append(tags, TagInfo{Name: name, Commit: tagCommits[name]})
	}
	return tags, nil
}
