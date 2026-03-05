package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/shnupta/workflow/internal/config"
	"github.com/shnupta/workflow/internal/daemon"
	"github.com/shnupta/workflow/internal/db"
	"github.com/shnupta/workflow/internal/handlers"
	"github.com/shnupta/workflow/internal/setup"
)

const usage = `workflow — a task board for leads

Usage:
  workflow <command> [flags]

Commands:
  setup      Interactive setup wizard (run this first)
  serve      Start the server in the foreground
  start      Start the background service
  stop       Stop the background service
  restart    Restart the background service
  status     Show service status
  update     Pull latest, rebuild, and restart

Run 'workflow <command> -help' for flags.
`

func main() {
	if len(os.Args) < 2 {
		// Default: start the server in foreground using the data dir
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	cmd := os.Args[1]

	switch cmd {
	case "setup":
		binary, _ := os.Executable()
		binary, _ = filepath.Abs(binary)
		if err := setup.Run(binary); err != nil {
			fmt.Fprintf(os.Stderr, "setup error: %v\n", err)
			os.Exit(1)
		}

	case "serve":
		cmdServe(os.Args[2:])

	case "start":
		mustInstalled()
		if err := daemon.Start(); err != nil {
			die(err)
		}
		// Brief pause then show status
		time.Sleep(500 * time.Millisecond)
		fmt.Println(daemon.Status())

	case "stop":
		mustInstalled()
		if err := daemon.Stop(); err != nil {
			die(err)
		}
		fmt.Println("stopped")

	case "restart":
		mustInstalled()
		if err := daemon.Restart(); err != nil {
			die(err)
		}
		time.Sleep(500 * time.Millisecond)
		fmt.Println(daemon.Status())

	case "status":
		fmt.Println(daemon.Status())

	case "update":
		cmdUpdate()

	case "-h", "-help", "--help", "help":
		fmt.Print(usage)

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n%s", cmd, usage)
		os.Exit(1)
	}
}

// cmdServe runs the HTTP server in the foreground.
func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr       := fs.String("addr", ":7070", "listen address")
	dir        := fs.String("dir", defaultDataDir(), "data directory (workflow.db and workflow.json)")
	tmplPath   := fs.String("templates", "", "template glob override (default: <binary dir>/templates/*.html)")
	_ = fs.Parse(args)

	if *tmplPath == "" {
		// Templates live next to the binary by default
		binary, _ := os.Executable()
		*tmplPath = filepath.Join(filepath.Dir(binary), "templates", "*.html")
	}

	// Support legacy -db / -config flags via env so old service plists still work
	dbPath     := filepath.Join(*dir, "workflow.db")
	configPath := filepath.Join(*dir, "workflow.json")

	if err := os.MkdirAll(*dir, 0755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	watcher, err := config.NewWatcher(configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	d, err := db.Open(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}

	h, err := handlers.New(d, watcher, *tmplPath)
	if err != nil {
		log.Fatalf("init handlers: %v", err)
	}

	mux := http.NewServeMux()
	h.Register(mux)

	// Static files: prefer <binary dir>/static, fall back to ./static
	staticDir := filepath.Join(filepath.Dir(mustExecutable()), "static")
	if _, err := os.Stat(staticDir); err != nil {
		staticDir = "./static"
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))

	log.Printf("workflow listening on %s  (data: %s)", *addr, *dir)
	if err := http.ListenAndServe(*addr, logMiddleware(mux)); err != nil {
		log.Fatal(err)
	}
}

// cmdUpdate pulls the latest code, rebuilds, and restarts the service.
func cmdUpdate() {
	// Find the repo — we look for the go.mod relative to the binary, or use $WORKFLOW_REPO.
	repoDir := os.Getenv("WORKFLOW_REPO")
	if repoDir == "" {
		// Try to find it: the binary might live inside the repo (dev mode)
		binary, _ := os.Executable()
		binary, _ = filepath.Abs(binary)
		dir := filepath.Dir(binary)
		for d := dir; d != "/" && d != "."; d = filepath.Dir(d) {
			if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
				repoDir = d
				break
			}
		}
	}

	if repoDir == "" {
		fmt.Fprintln(os.Stderr, "Could not find workflow repo. Set WORKFLOW_REPO=/path/to/repo and try again.")
		os.Exit(1)
	}

	fmt.Printf("Repo: %s\n\n", repoDir)

	steps := []struct {
		name string
		fn   func() error
	}{
		{"git pull", func() error {
			return runInDir(repoDir, "git", "pull")
		}},
		{"go build", func() error {
			binary, _ := os.Executable()
			binary, _ = filepath.Abs(binary)
			return runInDir(repoDir, "go", "build", "-tags", "fts5", "-o", binary, "./cmd/workflow/")
		}},
		{"restart service", func() error {
			if !daemon.IsInstalled() {
				fmt.Println("  (service not installed — skipping restart)")
				return nil
			}
			return daemon.Restart()
		}},
	}

	for _, s := range steps {
		fmt.Printf("  %-20s ", s.name+"...")
		if err := s.fn(); err != nil {
			fmt.Printf("✗\n    %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✓")
	}

	fmt.Println()
	fmt.Println("Updated. " + daemon.Status())
}

func runInDir(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func mustInstalled() {
	if !daemon.IsInstalled() {
		fmt.Fprintln(os.Stderr, "Service not installed. Run 'workflow setup' first.")
		os.Exit(1)
	}
}

func die(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".workflow")
}

func mustExecutable() string {
	p, err := os.Executable()
	if err != nil {
		return "."
	}
	p, _ = filepath.Abs(p)
	return p
}

// ── HTTP server helpers ──────────────────────────────────────────────────────

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)
		if strings.HasPrefix(r.URL.Path, "/static/") {
			return
		}
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rw.status, time.Since(start).Round(time.Millisecond))
	})
}
