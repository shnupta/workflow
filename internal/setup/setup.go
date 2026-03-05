// Package setup provides the interactive setup wizard for workflow.
package setup

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/shnupta/workflow/internal/config"
	"github.com/shnupta/workflow/internal/daemon"
)

const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorGreen  = "\033[32m"
	colorCyan   = "\033[36m"
	colorYellow = "\033[33m"
	colorDim    = "\033[2m"
	colorRed    = "\033[31m"
)

func bold(s string) string   { return colorBold + s + colorReset }
func green(s string) string  { return colorGreen + s + colorReset }
func cyan(s string) string   { return colorCyan + s + colorReset }
func yellow(s string) string { return colorYellow + s + colorReset }
func dim(s string) string    { return colorDim + s + colorReset }
func red(s string) string    { return colorRed + s + colorReset }

var scanner = bufio.NewScanner(os.Stdin)

func prompt(label, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("  %s %s: ", cyan(label), dim("["+defaultVal+"]"))
	} else {
		fmt.Printf("  %s: ", cyan(label))
	}
	scanner.Scan()
	v := strings.TrimSpace(scanner.Text())
	if v == "" {
		return defaultVal
	}
	return v
}

func yesNo(label string, defaultYes bool) bool {
	def := "Y/n"
	if !defaultYes {
		def = "y/N"
	}
	fmt.Printf("  %s %s: ", cyan(label), dim("["+def+"]"))
	scanner.Scan()
	v := strings.ToLower(strings.TrimSpace(scanner.Text()))
	if v == "" {
		return defaultYes
	}
	return v == "y" || v == "yes"
}

// findClaude looks for the claude binary in common locations and PATH.
func findClaude() string {
	// Check PATH first
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}

	candidates := []string{
		"/usr/local/bin/claude",
		"/opt/homebrew/bin/claude",
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".local/bin/claude"),
			filepath.Join(home, ".npm-global/bin/claude"),
			filepath.Join(home, ".nvm/versions/node/*/bin/claude"),
		)
	}
	for _, c := range candidates {
		// Handle glob patterns
		if strings.Contains(c, "*") {
			matches, _ := filepath.Glob(c)
			for _, m := range matches {
				if _, err := os.Stat(m); err == nil {
					return m
				}
			}
			continue
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// dataDir returns the recommended data directory for workflow.
func dataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".workflow")
}

// Run executes the interactive setup wizard.
// dataDir is where workflow.db and workflow.json will live.
// binaryPath is the absolute path of the workflow binary being run.
func Run(binaryPath string) error {
	fmt.Println()
	fmt.Println(bold("  workflow setup"))
	fmt.Println(dim("  ─────────────────────────────────────────────"))
	fmt.Println()

	// ── 1. Data directory ────────────────────────────────────────────────────

	fmt.Println(bold("  [1/4] Data directory"))
	fmt.Println(dim("  Where should workflow store its database and config?"))
	fmt.Println()
	dir := prompt("Directory", dataDir())
	dir, _ = filepath.Abs(dir)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	fmt.Printf("  %s %s\n\n", green("✓"), dir)

	// ── 2. Claude binary ─────────────────────────────────────────────────────

	fmt.Println(bold("  [2/4] Claude Code CLI"))
	fmt.Println(dim("  workflow uses Claude Code to power its AI sessions."))
	fmt.Println(dim("  Install it with: npm install -g @anthropic-ai/claude-code"))
	fmt.Println()

	claudeDefault := findClaude()
	if claudeDefault != "" {
		fmt.Printf("  %s found at %s\n", green("✓"), dim(claudeDefault))
	} else {
		fmt.Printf("  %s claude not found in PATH or common locations\n", yellow("!"))
	}
	fmt.Println()
	claudeBin := prompt("Claude binary path", claudeDefault)

	if claudeBin == "" {
		fmt.Printf("  %s No claude binary set — AI sessions won't work until you configure this.\n", yellow("⚠"))
		fmt.Println(dim("    You can fix it later by editing workflow.json in your data directory."))
	} else if _, err := os.Stat(claudeBin); err != nil {
		fmt.Printf("  %s File not found: %s\n", yellow("⚠"), claudeBin)
		fmt.Println(dim("    You can fix it later by editing workflow.json in your data directory."))
	} else {
		fmt.Printf("  %s %s\n", green("✓"), claudeBin)
	}
	fmt.Println()

	// ── 3. Work types & tiers ────────────────────────────────────────────────

	fmt.Println(bold("  [3/4] Board configuration"))
	fmt.Println(dim("  Using default tiers (Today / This Week / Backlog) and work types."))
	fmt.Println(dim("  You can edit these later in workflow.json."))
	fmt.Println()

	// Just show defaults and move on — no one wants to type 9 work types
	fmt.Println("  Tiers:")
	for _, t := range config.Default.Tiers {
		fmt.Printf("    %s %s\n", dim("•"), t.Label)
	}
	fmt.Println("  Work types:")
	for _, wt := range config.Default.WorkTypes {
		fmt.Printf("    %s %s %s\n", dim("•"), wt.Label, dim("("+wt.Depth+")"))
	}
	fmt.Println()

	// ── 4. Write config ───────────────────────────────────────────────────────

	cfgPath := filepath.Join(dir, "workflow.json")
	cfg := config.Default
	cfg.ClaudeBin = claudeBin

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(cfgPath, b, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	fmt.Printf("  %s Config written to %s\n\n", green("✓"), cfgPath)

	// ── 5. Daemon setup (macOS only) ──────────────────────────────────────────

	if runtime.GOOS == "darwin" {
		fmt.Println(bold("  [4/4] Background service"))
		fmt.Println(dim("  Install workflow as a launchd service so it starts automatically."))
		fmt.Println()

		if yesNo("Install launchd service (auto-start on login)", true) {
			if err := daemon.Install(binaryPath, dir); err != nil {
				fmt.Printf("  %s Failed to install service: %v\n", red("✗"), err)
				fmt.Println(dim("    You can start workflow manually with: workflow serve -dir " + dir))
			} else {
				fmt.Printf("  %s Plist written to ~/Library/LaunchAgents/\n", green("✓"))
				if yesNo("Start the service now", true) {
					if err := daemon.Start(); err != nil {
						fmt.Printf("  %s Failed to start: %v\n", red("✗"), err)
					} else {
						fmt.Printf("  %s Service started — open http://localhost:7070\n", green("✓"))
					}
				}
			}
		} else {
			fmt.Println()
			fmt.Println(dim("  Skipped. Start manually with:"))
			fmt.Printf("    %s\n", cyan("workflow serve -dir "+dir))
		}
	} else {
		fmt.Println(bold("  [4/4] Starting workflow"))
		fmt.Println(dim("  (launchd is macOS only — start manually on this platform)"))
		fmt.Println()
		fmt.Println(dim("  Run with:"))
		fmt.Printf("    %s\n", cyan("workflow serve -dir "+dir))
	}

	fmt.Println()
	fmt.Println(dim("  ─────────────────────────────────────────────"))
	fmt.Printf("  %s Setup complete!\n\n", bold(green("✓")))
	fmt.Printf("  Open %s in your browser\n", cyan("http://localhost:7070"))
	fmt.Printf("  Manage with: %s, %s, %s, %s\n",
		cyan("workflow status"),
		cyan("workflow restart"),
		cyan("workflow stop"),
		cyan("workflow update"),
	)
	fmt.Println()

	return nil
}
