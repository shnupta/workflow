package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/shnupta/workflow/internal/db"
	"github.com/shnupta/workflow/internal/handlers"
)

func main() {
	addr := flag.String("addr", ":7070", "listen address")
	dbPath := flag.String("db", "./workflow.db", "sqlite database path")
	tmplPath := flag.String("templates", "./templates/*.html", "template glob")
	flag.Parse()

	d, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}

	h, err := handlers.New(d, *tmplPath)
	if err != nil {
		log.Fatalf("init handlers: %v", err)
	}

	mux := http.NewServeMux()
	h.Register(mux)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))

	log.Printf("flow listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}
