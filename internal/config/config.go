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

const DefaultPRPrompt = `You are a senior engineer helping review a pull request. Analyse this PR diff and provide a concise, structured review brief.

PR URL: {{.PRURL}}

Format your response as:
## Summary
2-3 sentences on what this PR does.

## Key files to focus on
List the most important files/areas with a one-line note on why.

## Potential issues
Any bugs, logic errors, security concerns, or missing edge cases you spot. Be specific with line references where possible.

## Suggestions
Minor improvements, style notes, or things worth discussing.

---
DIFF:
{{.Diff}}`

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
	// API credentials
	GitHubToken   string `json:"github_token"`
	AnthropicKey  string `json:"anthropic_key"`
	ClaudeModel   string `json:"claude_model"`
	ClaudeBaseURL string `json:"claude_base_url"`

	// ClaudeMode controls how PR analysis is run:
	//   "api"   — call the Anthropic API directly (requires anthropic_key)
	//   "local" — run `claude -p --dangerously-skip-permissions --output-format json`
	//             Uses the local Claude Code CLI; no API key needed.
	ClaudeMode string `json:"claude_mode"`

	// ClaudeBin is the path to the claude CLI binary used for sessions.
	// Defaults to searching PATH for "claude".
	ClaudeBin string `json:"claude_bin"`

	// PR analysis prompt — use {{.PRURL}} and {{.Diff}} as placeholders
	PRPrompt string `json:"pr_prompt"`

	// Board config
	WorkTypes []WorkType `json:"work_types"`
	Tiers     []Tier     `json:"tiers"`
}

var Default = Config{
	GitHubToken:   "",
	AnthropicKey:  "",
	ClaudeModel:   "claude-opus-4-6",
	ClaudeBaseURL: "https://api.anthropic.com",
	ClaudeMode:    "api",
	PRPrompt:      DefaultPRPrompt,
	Tiers: []Tier{
		{Key: "today", Label: "Today", Order: 1},
		{Key: "this_week", Label: "This Week", Order: 2},
		{Key: "backlog", Label: "Backlog", Order: 3},
	},
	WorkTypes: []WorkType{
		{Key: "pr_review", Label: "PR Review", Depth: "deep"},
		{Key: "deployment", Label: "Deployment", Depth: "deep"},
		{Key: "coding", Label: "Coding", Depth: "deep"},
		{Key: "design", Label: "Design", Depth: "deep"},
		{Key: "doc", Label: "Doc", Depth: "medium"},
		{Key: "timeline", Label: "Timeline", Depth: "medium"},
		{Key: "approval", Label: "Approval", Depth: "shallow"},
		{Key: "chase", Label: "Chase", Depth: "shallow"},
		{Key: "meeting", Label: "Meeting", Depth: "shallow"},
		{Key: "misc", Label: "Misc", Depth: "shallow"},
	},
}

// Watcher holds the current config and reloads it when the file changes.
type Watcher struct {
	mu       sync.RWMutex
	cfg      *Config
	path     string
	lastMod  time.Time
}

// NewWatcher loads the config at path and starts watching it for changes.
func NewWatcher(path string) (*Watcher, error) {
	cfg, modTime, err := loadFile(path)
	if err != nil {
		return nil, err
	}
	w := &Watcher{cfg: cfg, path: path, lastMod: modTime}
	go w.watch()
	return w, nil
}

// Get returns the current config. Safe to call concurrently.
func (w *Watcher) Get() *Config {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.cfg
}

func (w *Watcher) watch() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		info, err := os.Stat(w.path)
		if err != nil {
			continue
		}
		if info.ModTime().After(w.lastMod) {
			cfg, modTime, err := loadFile(w.path)
			if err != nil {
				log.Printf("config reload error: %v", err)
				continue
			}
			w.mu.Lock()
			w.cfg = cfg
			w.lastMod = modTime
			w.mu.Unlock()
			log.Printf("config reloaded from %s", w.path)
		}
	}
}

// Load reads config from path, creating it with defaults if absent.
func Load(path string) (*Config, error) {
	cfg, _, err := loadFile(path)
	return cfg, err
}

func loadFile(path string) (*Config, time.Time, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := writeFile(path, &Default); err != nil {
			return nil, time.Time{}, fmt.Errorf("writing default config: %w", err)
		}
		fmt.Printf("Created default config at %s — add your API keys there\n", path)
		cfg := Default
		info, _ := os.Stat(path)
		return &cfg, info.ModTime(), nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, time.Time{}, err
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("opening config: %w", err)
	}
	defer f.Close()

	var cfg Config
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, time.Time{}, fmt.Errorf("parsing config: %w", err)
	}

	// Apply defaults for optional fields
	if cfg.ClaudeModel == "" {
		cfg.ClaudeModel = "claude-opus-4-6"
	}
	if cfg.ClaudeBaseURL == "" {
		cfg.ClaudeBaseURL = "https://api.anthropic.com"
	}
	if cfg.PRPrompt == "" {
		cfg.PRPrompt = DefaultPRPrompt
	}
	if cfg.ClaudeMode == "" {
		cfg.ClaudeMode = "api"
	}

	if err := validate(&cfg); err != nil {
		return nil, time.Time{}, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, info.ModTime(), nil
}

func writeFile(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(cfg)
}

func validate(cfg *Config) error {
	if len(cfg.Tiers) == 0 {
		return fmt.Errorf("must have at least one tier")
	}
	if len(cfg.WorkTypes) == 0 {
		return fmt.Errorf("must have at least one work type")
	}
	for _, wt := range cfg.WorkTypes {
		if wt.Depth != "deep" && wt.Depth != "medium" && wt.Depth != "shallow" {
			return fmt.Errorf("work type %q has invalid depth %q (must be deep, medium, or shallow)", wt.Key, wt.Depth)
		}
	}
	return nil
}

func (c *Config) TierKeys() []string {
	keys := make([]string, len(c.Tiers))
	for i, t := range c.Tiers {
		keys[i] = t.Key
	}
	return keys
}

func (c *Config) TierByKey(key string) *Tier {
	for i := range c.Tiers {
		if c.Tiers[i].Key == key {
			return &c.Tiers[i]
		}
	}
	return nil
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
