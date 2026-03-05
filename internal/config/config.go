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
