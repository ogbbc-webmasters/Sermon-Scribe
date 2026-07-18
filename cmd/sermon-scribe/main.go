// Command sermon-scribe runs the Sermon Scribe web server.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/processing"
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	events := server.NewEventHub()
	// Stage implementations register handlers here. Spec 0.2 adds the
	// production normalize handler; unknown job types remain safely queued.
	queue := processing.NewQueue(st, map[string]processing.Handler{}, processing.Config{Events: events})
	queue.Start(ctx)
	defer queue.Stop()

	app := &server.Server{
		Store: st, UploadsDir: *uploadsDir, Events: events, Queue: queue,
	}
	httpServer := &http.Server{Addr: *addr, Handler: app.Routes(web.WebFS())}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("server shutdown: %v", err)
		}
	}()

	log.Printf("listening on %s", *addr)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
