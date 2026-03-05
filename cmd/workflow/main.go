package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/shnupta/workflow/internal/config"
	"github.com/shnupta/workflow/internal/db"
	"github.com/shnupta/workflow/internal/handlers"
)

func main() {
	addr       := flag.String("addr", ":7070", "listen address")
	dbPath     := flag.String("db", "./workflow.db", "sqlite database path")
	tmplPath   := flag.String("templates", "./templates/*.html", "template glob")
	configPath := flag.String("config", "./workflow.json", "config file (created with defaults if absent)")
	flag.Parse()

	watcher, err := config.NewWatcher(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	d, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}

	h, err := handlers.New(d, watcher, *tmplPath)
	if err != nil {
		log.Fatalf("init handlers: %v", err)
	}

	mux := http.NewServeMux()
	h.Register(mux)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))

	log.Printf("workflow listening on %s", *addr)
	if err := http.ListenAndServe(*addr, logMiddleware(mux)); err != nil {
		log.Fatal(err)
	}
}

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
		// Skip static asset noise
		if len(r.URL.Path) > 8 && r.URL.Path[:8] == "/static/" {
			return
		}
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rw.status, time.Since(start).Round(time.Millisecond))
	})
}
