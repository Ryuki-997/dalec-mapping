// ═══════════════════════════════════════════════════════════════════════════════
// Step 8 — Notify Owners
//
//   Sends email notifications to reviewers listed in the onboard config
//   via the Microsoft Graph API (/users/{from}/sendMail).
//
//   Two notification types:
//     - Review request: first-time onboard, requests owners to review the spec
//     - Content change: Dockerfile/Makefile changed, notifies owners
//
//   Chunk 1 · MAIN    NotifyOwners()
//   Chunk 2 · HELPERS buildReviewEmail(), buildContentChangeEmail(), sendEmail()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"dalec-mapping/domain/onboarding"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// ─── Chunk 1 · MAIN ─────────────────────────────────────────────────────────

// NotifyOwners sends email notifications to all reviewers based on the onboard state.
// For first-time onboards: sends a review request email.
// For content changes on re-onboard: sends a content-change notification.
func NotifyOwners(onboard *onboarding.OnboardingInfo, tag string, isFirstOnboard bool) error {
	if len(onboard.Reviewers) == 0 {
		log.Printf("⚠️  No reviewers configured for %s — skipping notification\n", onboard.SpecImageName)
		return nil
	}

	var subject, body string

	if isFirstOnboard {
		subject, body = buildReviewEmail(onboard, tag)
	} else {
		subject, body = buildContentChangeEmail(onboard, tag)
	}

	if err := sendEmail(onboard.Reviewers, subject, body); err != nil {
		return fmt.Errorf("failed to send notification to reviewers: %w", err)
	}

	log.Printf("✅ Notification sent to %d reviewer(s) for %s @ %s\n", len(onboard.Reviewers), onboard.SpecImageName, tag)
	return nil
}

// ─── Chunk 2 · HELPERS ──────────────────────────────────────────────────────

// buildReviewEmail constructs a review request email for first-time onboards.
func buildReviewEmail(onboard *onboarding.OnboardingInfo, tag string) (subject, body string) {
	subject = fmt.Sprintf("[Dalec] Review Requested: %s @ %s", onboard.SpecImageName, tag)

	body = fmt.Sprintf(`A new Dalec spec has been generated and pushed for review.

Repository:  %s
Image:       %s
Tag:         %s

This is a first-time onboard. Please review the generated spec to ensure correctness before it is promoted.

Reviewers: %s
`, onboard.Repository, onboard.SpecImageName, tag, strings.Join(onboard.Reviewers, ", "))

	return subject, body
}

// buildContentChangeEmail constructs a notification for Dockerfile/Makefile changes.
func buildContentChangeEmail(onboard *onboarding.OnboardingInfo, tag string) (subject, body string) {
	subject = fmt.Sprintf("[Dalec] Content Changed: %s @ %s", onboard.SpecImageName, tag)

	body = fmt.Sprintf(`The Dockerfile or Makefile has changed for a previously onboarded spec.

Repository:  %s
Image:       %s
Tag:         %s

The spec has been regenerated to reflect the updated build files. Please review the changes.
`, onboard.Repository, onboard.SpecImageName, tag)

	return subject, body
}

// sendEmail sends an email via the Microsoft Graph API.
// When GRAPH_SENDER_ID is set, sends via /users/{id}/sendMail (application permissions, for CI/CD).
// Otherwise, sends via /me/sendMail (delegated permissions, for local dev with az login).
func sendEmail(recipients []string, subject, body string) error {
	// Build recipient list
	toRecipients := make([]map[string]interface{}, len(recipients))
	for i, r := range recipients {
		toRecipients[i] = map[string]interface{}{
			"emailAddress": map[string]string{"address": r},
		}
	}

	payload := map[string]interface{}{
		"message": map[string]interface{}{
			"subject": subject,
			"body": map[string]string{
				"contentType": "Text",
				"content":     body,
			},
			"toRecipients": toRecipients,
		},
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal email payload: %w", err)
	}

	// Acquire token — use the registered application's managed identity when
	// APPLICATION_CLIENT_ID is set, otherwise fall back to DefaultAzureCredential.
	appClientID := os.Getenv("APPLICATION_CLIENT_ID")

	tokenOpts := policy.TokenRequestOptions{
		Scopes: []string{"https://graph.microsoft.com/.default"},
	}

	var tokenVal string
	if appClientID != "" {
		cred, err := azidentity.NewManagedIdentityCredential(&azidentity.ManagedIdentityCredentialOptions{
			ID: azidentity.ClientID(appClientID),
		})
		if err != nil {
			return fmt.Errorf("failed to create managed identity credential for app %s: %w", appClientID, err)
		}
		token, err := cred.GetToken(context.TODO(), tokenOpts)
		if err != nil {
			return fmt.Errorf("failed to acquire Graph API token for app %s: %w", appClientID, err)
		}
		tokenVal = token.Token
	} else {
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return fmt.Errorf("failed to create Azure credential: %w", err)
		}
		token, err := cred.GetToken(context.TODO(), tokenOpts)
		if err != nil {
			return fmt.Errorf("failed to acquire Graph API token: %w", err)
		}
		tokenVal = token.Token
	}

	// Always use /me/sendMail — the app registration's identity is the sender.
	url := "https://graph.microsoft.com/v1.0/me/sendMail"

	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tokenVal)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("Graph API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Graph API returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
