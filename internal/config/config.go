package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	// API credentials
	GitHubToken   string `json:"github_token"`
	AnthropicKey  string `json:"anthropic_key"`
	ClaudeModel   string `json:"claude_model"`
	ClaudeBaseURL string `json:"claude_base_url"`

	// Board config
	WorkTypes []WorkType `json:"work_types"`
	Tiers     []Tier     `json:"tiers"`
}

var Default = Config{
	GitHubToken:   "",
	AnthropicKey:  "",
	ClaudeModel:   "claude-opus-4-6",
	ClaudeBaseURL: "https://api.anthropic.com",
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

// Load reads config from path. If the file doesn't exist, writes the default
// config there (with empty credentials) and returns it.
func Load(path string) (*Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := write(path, &Default); err != nil {
			return nil, fmt.Errorf("writing default config: %w", err)
		}
		fmt.Printf("Created default config at %s — add your API keys there\n", path)
		cfg := Default
		return &cfg, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening config: %w", err)
	}
	defer f.Close()

	var cfg Config
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	// Apply defaults for optional fields
	if cfg.ClaudeModel == "" {
		cfg.ClaudeModel = "claude-opus-4-6"
	}
	if cfg.ClaudeBaseURL == "" {
		cfg.ClaudeBaseURL = "https://api.anthropic.com"
	}

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

func write(path string, cfg *Config) error {
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
