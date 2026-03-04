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
	Order int    `json:"order"` // lower = higher priority, used for board column order
}

type Config struct {
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

// Load reads config from path. If the file doesn't exist, it writes the
// default config there first, then returns it.
func Load(path string) (*Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := writeDefault(path); err != nil {
			return nil, fmt.Errorf("writing default config: %w", err)
		}
		fmt.Printf("Created default config at %s\n", path)
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

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

func writeDefault(path string) error {
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
	return enc.Encode(Default)
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

// TierKeys returns all tier keys in order.
func (c *Config) TierKeys() []string {
	keys := make([]string, len(c.Tiers))
	for i, t := range c.Tiers {
		keys[i] = t.Key
	}
	return keys
}

// TierByKey finds a tier by key.
func (c *Config) TierByKey(key string) *Tier {
	for i := range c.Tiers {
		if c.Tiers[i].Key == key {
			return &c.Tiers[i]
		}
	}
	return nil
}

// WorkTypeByKey finds a work type by key.
func (c *Config) WorkTypeByKey(key string) *WorkType {
	for i := range c.WorkTypes {
		if c.WorkTypes[i].Key == key {
			return &c.WorkTypes[i]
		}
	}
	return nil
}

// WorkTypeDepth returns the depth for a given work type key, defaulting to medium.
func (c *Config) WorkTypeDepth(key string) string {
	if wt := c.WorkTypeByKey(key); wt != nil {
		return wt.Depth
	}
	return "medium"
}
