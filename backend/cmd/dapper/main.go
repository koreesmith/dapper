package main

import (
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/koreesmith/dapper/backend/internal/api"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	static := flag.String("static", "", "directory of built web UI to serve (optional)")
	flag.Parse()

	var fs http.Handler
	if *static != "" {
		if _, err := os.Stat(*static); err != nil {
			log.Fatalf("static dir: %v", err)
		}
		fs = http.FileServer(http.Dir(*static))
	}
	log.Printf("dapper listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, api.New().Handler(fs)))
}
