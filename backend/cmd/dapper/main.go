package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/koreesmith/dapper/backend/internal/api"
	"github.com/koreesmith/dapper/backend/internal/config"
	"github.com/koreesmith/dapper/backend/internal/policy"
)

// version is set at build time: -ldflags "-X main.version=2.0.0". "dev" means a local build.
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	path := flag.String("config", envOr("DAPPER_CONFIG", "dapper.yaml"), "optional YAML file (everything can be set in the web UI instead)")
	flag.Parse()
	if *showVersion {
		fmt.Println("dapper", version)
		return
	}
	api.Version = version

	store, err := config.Open(*path)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cfg := store.Get()

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

	srv := api.New(store, pol)
	if tok := srv.SetupToken(); tok != "" {
		log.Printf("=====================================================================")
		log.Printf("Dapper is not set up yet. Open it in your browser to run the setup")
		log.Printf("wizard, and enter this setup code when asked:  %s", tok)
		log.Printf("=====================================================================")
	}
	log.Printf("dapper %s listening on %s", version, cfg.Listen)
	log.Fatal(http.ListenAndServe(cfg.Listen, srv.Handler(static)))
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
