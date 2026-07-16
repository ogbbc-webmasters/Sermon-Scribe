package main

import (
	"database/sql"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"

	_ "modernc.org/sqlite"
)

//go:embed web
var webFS embed.FS

func main() {
	addr := flag.String("addr", ":8000", "listen address")
	dbPath := flag.String("db", "sermons.db", "path to SQLite database file")
	uploadsDir := flag.String("uploads", "uploads", "directory for uploaded files")
	flag.Parse()

	db, err := openDB(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	srv := &server{db: db, uploadsDir: *uploadsDir}

	log.Printf("listening on %s", *addr)
	if err := http.ListenAndServe(*addr, srv.routes()); err != nil {
		log.Fatal(err)
	}
}

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if _, err := db.Exec(`PRAGMA journal_mode = WAL; PRAGMA foreign_keys = ON;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure database: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

type server struct {
	db         *sql.DB
	uploadsDir string
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	web, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServerFS(web))
	return mux
}
