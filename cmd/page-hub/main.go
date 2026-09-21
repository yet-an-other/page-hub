package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yet-an-other/page-hub/internal/config"
	"github.com/yet-an-other/page-hub/internal/server"
	"github.com/yet-an-other/page-hub/internal/storage"
)

var version = "dev"

func main() {
	versionFlag := flag.Bool("version", false, "print the Page Hub version")
	flag.Parse()
	if *versionFlag {
		fmt.Println(version)
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "page-hub configuration error: %v\n", err)
		os.Exit(2)
	}
	cfg.Version = version

	application, err := server.New(cfg, storage.NewS3Checker(cfg.Storage), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "page-hub startup error: %v\n", err)
		os.Exit(2)
	}
	listener, cleanup, err := server.Listen(cfg.ListenAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "page-hub listener error: %v\n", err)
		os.Exit(2)
	}
	defer cleanup()

	httpServer := &http.Server{
		Handler:           application.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	shutdownContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-shutdownContext.Done()
		gracefulContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(gracefulContext)
	}()

	slog.Info("page-hub manager listening", "version", version, "address", cfg.ListenAddr)
	if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("page-hub manager stopped", "error", err)
		os.Exit(1)
	}
}
