package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/shnupta/workflow/internal/models"
)

// registerWebhookRoutes registers GitHub webhook endpoints.
func (h *Handler) registerWebhookRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /webhooks/github", h.githubWebhook)
}

// githubWebhook handles incoming GitHub webhook events.
// It processes pull_request events (opened, synchronize, reopened) and creates
// or updates a workflow task accordingly.
func (h *Handler) githubWebhook(w http.ResponseWriter, r *http.Request) {
	// Read body first — needed for signature verification
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		http.Error(w, "read error", 500)
		return
	}

	// Verify signature if secret is configured
	secret := h.cfg().WebhookSecret
	if secret != "" {
		sig := r.Header.Get("X-Hub-Signature-256")
		if !verifyGitHubSignature(body, secret, sig) {
			http.Error(w, "invalid signature", 401)
			return
		}
	}

	event := r.Header.Get("X-GitHub-Event")
	delivery := r.Header.Get("X-GitHub-Delivery")
	log.Printf("webhook: %s event, delivery=%s", event, delivery)

	switch event {
	case "pull_request":
		h.handlePREvent(w, body)
	case "ping":
		// GitHub sends a ping when the webhook is first configured
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "pong")
	default:
		// Ignore other events — return 200 so GitHub doesn't retry
		w.WriteHeader(http.StatusOK)
	}
}

// prPayload is the minimal subset of a GitHub pull_request event we care about.
type prPayload struct {
	Action string `json:"action"`
	Number int    `json:"number"`
	PullRequest struct {
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		State   string `json:"state"`
		Draft   bool   `json:"draft"`
		Head    struct {
			Ref string `json:"ref"` // branch name
		} `json:"head"`
		Base struct {
			Ref  string `json:"ref"` // target branch
			Repo struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"base"`
	} `json:"pull_request"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

func (h *Handler) handlePREvent(w http.ResponseWriter, body []byte) {
	var payload prPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "bad json", 400)
		return
	}

	// Only handle open/reopen/sync; ignore closed, assigned, labeled, etc.
	switch payload.Action {
	case "opened", "reopened", "synchronize", "ready_for_review":
		// continue
	default:
		w.WriteHeader(http.StatusOK)
		return
	}

	pr := payload.PullRequest
	if pr.HTMLURL == "" {
		http.Error(w, "missing pr url", 400)
		return
	}

	// Skip draft PRs (can be made configurable later)
	if pr.Draft && payload.Action == "opened" {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "skipped draft")
		return
	}

	// Check if a task already exists for this PR URL
	existing, err := h.db.GetTaskByPRURL(pr.HTMLURL)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	repo := payload.Repository.FullName
	if repo == "" {
		repo = pr.Base.Repo.FullName
	}

	title := fmt.Sprintf("Review: %s #%d", repo, payload.Number)
	if pr.Title != "" {
		title = fmt.Sprintf("Review: %s", pr.Title)
	}

	if existing != nil {
		// Task exists — update title if the PR title changed, re-run brief on sync
		updated := false
		if existing.Title != title {
			existing.Title = title
			updated = true
		}
		if updated {
			if err := h.db.UpdateTask(existing); err != nil {
				log.Printf("webhook: update task: %v", err)
			}
		}
		if payload.Action == "synchronize" {
			// New commits pushed — re-run the brief
			go h.runAutoBrief(existing)
		}
		log.Printf("webhook: updated task %s for PR %s (action=%s)", existing.ID, pr.HTMLURL, payload.Action)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "updated task %s", existing.ID)
		return
	}

	// No existing task — create a new one
	description := formatPRDescription(pr.Body, pr.Head.Ref, pr.Base.Ref)

	// Use first tier (Today) for webhook-created tasks since they're incoming work
	tier := "today"
	if len(h.cfg().Tiers) > 0 {
		tier = h.cfg().Tiers[0].Key
	}

	t := &models.Task{
		Title:       title,
		Description: description,
		WorkType:    "pr_review",
		Tier:        tier,
		Direction:   "blocked_on_me",
		PRURL:       pr.HTMLURL,
	}
	if err := h.db.CreateTask(t); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	go h.runAutoBrief(t)

	log.Printf("webhook: created task %s for PR %s", t.ID, pr.HTMLURL)
	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, "created task %s", t.ID)
}

// formatPRDescription builds a short description from the PR body and branch info.
func formatPRDescription(body, headRef, baseRef string) string {
	var parts []string
	if headRef != "" && baseRef != "" {
		parts = append(parts, fmt.Sprintf("%s → %s", headRef, baseRef))
	}
	// Take first non-empty line of PR body as description
	if body != "" {
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			// Skip markdown headings, checkboxes, empty lines
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "- [") {
				continue
			}
			if len(line) > 200 {
				line = line[:197] + "..."
			}
			parts = append(parts, line)
			break
		}
	}
	return strings.Join(parts, " · ")
}

// verifyGitHubSignature checks the HMAC-SHA256 signature from GitHub.
func verifyGitHubSignature(body []byte, secret, signature string) bool {
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	sig, err := hex.DecodeString(signature[7:])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := mac.Sum(nil)
	return hmac.Equal(sig, expected)
}
