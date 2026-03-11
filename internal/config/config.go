package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type WorkType struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Depth string `json:"depth"` // "deep", "medium", "shallow"
}

type Tier struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Order int    `json:"order"`
}

type Config struct {
	// ClaudeBin is the path to the claude CLI binary used for sessions.
	// Defaults to searching PATH for "claude".
	ClaudeBin string `json:"claude_bin"`

	// AgentProvider selects the runner: "claude_local" (default) or "claude_api".
	// If empty, claude_local is used with automatic fallback to claude_api
	// if the CLI is not found and anthropic_api_key is set.
	AgentProvider string `json:"agent_provider,omitempty"`

	// AnthropicAPIKey is used by the claude_api provider.
	// Never commit this to git — set it in workflow.json which is gitignored.
	AnthropicAPIKey string `json:"anthropic_api_key,omitempty"`

	// AgentModel overrides the default model for the claude_api provider.
	// Defaults to claude-sonnet-4-5.
	AgentModel string `json:"agent_model,omitempty"`

	// AgentSystemPrompt is an optional system prompt injected for all claude_api sessions.
	AgentSystemPrompt string `json:"agent_system_prompt,omitempty"`

	// WebhookSecret is the GitHub webhook secret for verifying incoming PR events.
	// Set this to the same value you enter in GitHub's webhook settings.
	// If empty, webhook signature verification is skipped (not recommended for production).
	WebhookSecret string `json:"webhook_secret"`

	// SprintGoal is the number of tasks the user wants to complete this week.
	// 0 means no goal is set (progress bar hidden). Resets manually or via UI.
	SprintGoal int `json:"sprint_goal,omitempty"`

	// Board config
	WorkTypes []WorkType `json:"work_types"`
	Tiers     []Tier     `json:"tiers"`
}

var Default = Config{
	Tiers: []Tier{
		{Key: "today", Label: "Today", Order: 1},
		{Key: "this_week", Label: "This Week", Order: 2},
		{Key: "backlog", Label: "Backlog", Order: 3},
	},
	WorkTypes: []WorkType{
		{Key: "pr_review",  Label: "PR Review",  Depth: "medium"},
		{Key: "deployment", Label: "Deployment",  Depth: "shallow"},
		{Key: "coding",     Label: "Coding",      Depth: "deep"},
		{Key: "design",     Label: "Design",      Depth: "deep"},
		{Key: "docs",       Label: "Docs",        Depth: "shallow"},
		{Key: "meeting",    Label: "Meeting",      Depth: "shallow"},
		{Key: "approval",   Label: "Approval",    Depth: "shallow"},
		{Key: "chase",      Label: "Chase",       Depth: "shallow"},
		{Key: "other",      Label: "Other",       Depth: "medium"},
	},
}

func (c *Config) WorkTypeByKey(key string) *WorkType {
	for i := range c.WorkTypes {
		if c.WorkTypes[i].Key == key {
			return &c.WorkTypes[i]
		}
	}
	return nil
}

func (c *Config) WorkTypeDepth(key string) string {
	if wt := c.WorkTypeByKey(key); wt != nil {
		return wt.Depth
	}
	return "medium"
}

func (c *Config) WorkTypeLabel(key string) string {
	if wt := c.WorkTypeByKey(key); wt != nil {
		return wt.Label
	}
	return key
}

func (c *Config) TierByKey(key string) *Tier {
	for i := range c.Tiers {
		if c.Tiers[i].Key == key {
			return &c.Tiers[i]
		}
	}
	return nil
}

// ── File watcher ────────────────────────────────────────────────────────────

type Watcher struct {
	mu       sync.RWMutex
	current  *Config
	path     string
	lastMod  time.Time
}

func NewWatcher(path string) (*Watcher, error) {
	w := &Watcher{path: path}
	cfg, err := w.load()
	if err != nil {
		return nil, err
	}
	w.current = cfg

	go func() {
		for range time.Tick(2 * time.Second) {
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			if info.ModTime().After(w.lastMod) {
				cfg, err := w.load()
				if err != nil {
					log.Printf("config: reload error: %v", err)
					continue
				}
				w.mu.Lock()
				w.current = cfg
				w.mu.Unlock()
				w.lastMod = info.ModTime()
				log.Printf("config: reloaded %s", filepath.Base(path))
			}
		}
	}()
	return w, nil
}

func (w *Watcher) Get() *Config {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.current
}

func (w *Watcher) load() (*Config, error) {
	_, err := os.Stat(w.path)
	if os.IsNotExist(err) {
		if err := w.writeDefaults(); err != nil {
			return nil, fmt.Errorf("create default config: %w", err)
		}
		log.Printf("config: created default %s", w.path)
		c := Default
		return &c, nil
	}
	f, err := os.Open(w.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	c := Default // start from defaults so new fields get sensible values
	if err := json.NewDecoder(f).Decode(&c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", w.path, err)
	}
	return &c, nil
}

func (w *Watcher) writeDefaults() error {
	b, err := json.MarshalIndent(Default, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(w.path, b, 0644)
}

// Patch applies a mutating function to the current config and writes it back
// to disk atomically. The hot-reload loop will pick it up within 2s.
func (w *Watcher) Patch(fn func(*Config)) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	// clone
	b, err := json.Marshal(w.current)
	if err != nil {
		return err
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return err
	}
	fn(&cfg)
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(w.path, out, 0644); err != nil {
		return err
	}
	w.current = &cfg
	return nil
}
