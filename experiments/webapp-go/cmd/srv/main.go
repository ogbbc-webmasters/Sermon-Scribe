package main

import (
	"flag"
	"fmt"
	"os"

	"srv.exe.dev/srv"
)

var (
	flagListenAddr = flag.String("listen", ":8000", "address to listen on")
	flagDBPath     = flag.String("db", "sermon-scribe.db", "path to SQLite database")
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	flag.Parse()

	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("OPENROUTER_API_KEY environment variable required")
	}

	db, err := srv.OpenDB(*flagDBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

	server := srv.New(apiKey, db)
	return server.Serve(*flagListenAddr)
}
