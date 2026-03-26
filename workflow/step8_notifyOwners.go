// ═══════════════════════════════════════════════════════════════════════════════
// Step 8 — Notify Owners
//
//   Sends email notifications to owners listed in the onboard config.
//   Two notification types:
//     - Review request: first-time onboard, requests owners to review the spec
//     - Content change: Dockerfile/Makefile changed, notifies owners
//
//   Chunk 1 · MAIN    NotifyOwners()
//   Chunk 2 · HELPERS buildReviewEmail(), buildContentChangeEmail(), sendEmail()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

import (
	"fmt"
	"log"
	"net/smtp"
	"os"
	"strings"

	"dalec-mapping/domain/onboarding"
)

// ─── Chunk 1 · MAIN ─────────────────────────────────────────────────────────

// NotifyOwners sends email notifications to all owners based on the onboard mode.
// For FirstOnboard: sends a review request email asking owners to review the generated spec.
// For ContentChanged: sends a notification that Dockerfile/Makefile content has changed.
func NotifyOwners(onboard *onboarding.OnboardingInfo, tag string) error {
	if len(onboard.Reviewers) == 0 {
		log.Printf("⚠️  No reviewers configured for %s — skipping notification\n", onboard.SpecImageName)
		return nil
	}

	var subject, body string

	switch onboard.Mode {
	case onboarding.FirstOnboard:
		subject, body = buildReviewEmail(onboard, tag)
	case onboarding.ContentChanged:
		subject, body = buildContentChangeEmail(onboard, tag)
	default:
		// CommitBump — no notification needed
		return nil
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

// sendEmail sends an email to the specified recipients via SMTP.
// Requires SMTP_HOST, SMTP_PORT, SMTP_FROM environment variables.
func sendEmail(recipients []string, subject, body string) error {
	host := os.Getenv("SMTP_HOST")
	port := os.Getenv("SMTP_PORT")
	from := os.Getenv("SMTP_FROM")

	if host == "" || port == "" || from == "" {
		log.Printf("⚠️  SMTP not configured (SMTP_HOST, SMTP_PORT, SMTP_FROM) — logging notification instead")
		log.Printf("  To: %s", strings.Join(recipients, ", "))
		log.Printf("  Subject: %s", subject)
		log.Printf("  Body:\n%s", body)
		return nil
	}

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s",
		from,
		strings.Join(recipients, ", "),
		subject,
		body,
	)

	addr := host + ":" + port
	if err := smtp.SendMail(addr, nil, from, recipients, []byte(msg)); err != nil {
		return fmt.Errorf("smtp send failed: %w", err)
	}

	return nil
}
