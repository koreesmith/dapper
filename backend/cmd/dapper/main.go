package main

import (
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/koreesmith/dapper/backend/internal/api"
	"github.com/koreesmith/dapper/backend/internal/config"
	"github.com/koreesmith/dapper/backend/internal/policy"
)

func main() {
	path := flag.String("config", envOr("DAPPER_CONFIG", "dapper.yaml"), "path to config file")
	flag.Parse()

	cfg, err := config.Load(*path)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	pol, err := policy.Load(cfg.DataDir)
	if err != nil {
		log.Fatalf("policy: %v", err)
	}

	var static http.Handler
	if cfg.Static != "" {
		if _, err := os.Stat(cfg.Static); err != nil {
			log.Fatalf("static dir: %v", err)
		}
		static = http.FileServer(http.Dir(cfg.Static))
	}
	log.Printf("dapper listening on %s", cfg.Listen)
	log.Fatal(http.ListenAndServe(cfg.Listen, api.New(cfg, pol).Handler(static)))
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
