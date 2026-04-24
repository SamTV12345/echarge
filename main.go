package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"echarge/internal/build"
	"echarge/internal/data"
	"echarge/internal/osrm"
	"echarge/internal/route"
	"echarge/internal/web"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	srcDir := flag.String("src", "web/src", "TypeScript source directory")
	osrmURL := flag.String("osrm", "https://router.project-osrm.org",
		"OSRM base URL for route planning (set empty to disable)")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Printf("startup: parsing embedded registry…")
	store, err := data.Load(ctx)
	if err != nil {
		log.Fatalf("load registry: %v", err)
	}
	defer store.Close()

	log.Printf("startup: bundling %s/main.ts with esbuild…", *srcDir)
	jsBundle := build.MustBuildJS(*srcDir)
	log.Printf("startup: bundle ready (%d bytes)", len(jsBundle))

	srv := &web.Server{Store: store, JSBundle: jsBundle}
	if *osrmURL != "" {
		srv.Planner = &route.Planner{
			Store: store,
			OSRM:  osrm.New(*osrmURL, nil),
		}
		log.Printf("startup: route planner enabled (OSRM %s)", *osrmURL)
	}
	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("listening on http://localhost%s", *addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("shutdown: draining connections…")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	if err := httpSrv.Shutdown(shutCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
