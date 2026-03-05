// Package daemon manages workflow as a launchd service on macOS.
// It writes a launchd plist to ~/Library/LaunchAgents/ and provides
// start / stop / restart / status helpers.
package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

const label = "com.workflow.server"

var plistTmpl = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.Binary}}</string>
		<string>serve</string>
		<string>-dir</string>
		<string>{{.Dir}}</string>
	</array>
	<key>WorkingDirectory</key>
	<string>{{.Dir}}</string>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>{{.Dir}}/workflow.log</string>
	<key>StandardErrorPath</key>
	<string>{{.Dir}}/workflow.log</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>HOME</key>
		<string>{{.Home}}</string>
		<key>PATH</key>
		<string>{{.Path}}</string>
	</dict>
</dict>
</plist>
`))

type plistData struct {
	Label  string
	Binary string
	Dir    string
	Home   string
	Path   string
}

func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist"), nil
}

// Install writes the launchd plist. binary is the absolute path to the workflow
// binary; dir is the data directory (where workflow.db and workflow.json live).
func Install(binary, dir string) error {
	p, err := plistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}

	home, _ := os.UserHomeDir()
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin"
	}

	f, err := os.Create(p)
	if err != nil {
		return fmt.Errorf("create plist: %w", err)
	}
	defer f.Close()

	return plistTmpl.Execute(f, plistData{
		Label:  label,
		Binary: binary,
		Dir:    dir,
		Home:   home,
		Path:   path,
	})
}

// Start loads the launchd job (starts the service).
func Start() error {
	p, err := plistPath()
	if err != nil {
		return err
	}
	// If already loaded, just kick it; otherwise bootstrap it
	out, err := exec.Command("launchctl", "list", label).CombinedOutput()
	if err == nil && len(out) > 0 && !strings.Contains(string(out), "Could not find") {
		return runLaunchctl("kickstart", "-k", "gui/"+uid()+"/"+label)
	}
	return runLaunchctl("load", p)
}

// Stop unloads the launchd job.
func Stop() error {
	p, err := plistPath()
	if err != nil {
		return err
	}
	return runLaunchctl("unload", p)
}

// Restart stops then starts.
func Restart() error {
	_ = Stop()
	return Start()
}

// Status returns a human-readable status string.
func Status() string {
	out, err := exec.Command("launchctl", "list", label).CombinedOutput()
	if err != nil || strings.Contains(string(out), "Could not find") {
		p, _ := plistPath()
		if _, err := os.Stat(p); err == nil {
			return "stopped  (plist installed, service not loaded)"
		}
		return "not installed"
	}
	// Parse PID from launchctl list output
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "{") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 1 && fields[0] != "-" {
			return fmt.Sprintf("running  (pid %s)", fields[0])
		}
	}
	return "loaded (not running)"
}

// IsInstalled reports whether the plist file exists.
func IsInstalled() bool {
	p, err := plistPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

func runLaunchctl(args ...string) error {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}

func uid() string {
	out, _ := exec.Command("id", "-u").Output()
	return strings.TrimSpace(string(out))
}
