// Command sermon-scribe runs the Sermon Scribe web server.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/server"
	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
	"github.com/ogbbc-webmasters/Sermon-Scribe/web"
)

func main() {
	addr := flag.String("addr", ":8000", "listen address")
	dbPath := flag.String("db", "sermons.db", "path to SQLite database file")
	uploadsDir := flag.String("uploads", "uploads", "directory for uploaded files")
	flag.Parse()

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	srv := &server.Server{Store: st, UploadsDir: *uploadsDir}

	log.Printf("listening on %s", *addr)
	if err := http.ListenAndServe(*addr, srv.Routes(web.WebFS())); err != nil {
		log.Fatal(err)
	}
}
