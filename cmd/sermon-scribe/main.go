// Command sermon-scribe runs the Sermon Scribe web server.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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

	interruptedUploads, err := st.DiscardInterruptedUploads()
	if err != nil {
		log.Fatal(err)
	}
	for _, id := range interruptedUploads {
		if err := os.RemoveAll(filepath.Join(*uploadsDir, id)); err != nil {
			log.Printf("remove interrupted upload %s: %v", id, err)
		}
	}
	app := &server.Server{Store: st, UploadsDir: *uploadsDir}
	if err := app.ReconcileTimelineArtifacts(); err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	events := server.NewEventHub()
	normalize := processing.NewNormalizeHandler(st, *uploadsDir)
	applyEdits := processing.NewApplyEditsHandler(st, *uploadsDir)
	queue := processing.NewQueue(st, map[string]processing.Handler{
		"normalize":   normalize,
		"apply_edits": applyEdits,
	}, processing.Config{Events: events})
	queue.Start(ctx)
	defer queue.Stop()

	app.Events = events
	app.Queue = queue
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
