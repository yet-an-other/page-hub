package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yet-an-other/page-hub/internal/adoption"
	"github.com/yet-an-other/page-hub/internal/catalog"
	"github.com/yet-an-other/page-hub/internal/config"
	"github.com/yet-an-other/page-hub/internal/server"
	"github.com/yet-an-other/page-hub/internal/storage"
)

var version = "dev"

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "serve":
			runServe(os.Args[2:])
			return
		case "migrate":
			runMigrate(os.Args[2:])
			return
		case "plan":
			runPlan(os.Args[2:])
			return
		case "commit":
			runCommit(os.Args[2:])
			return
		case "version":
			printVersion()
			return
		}
	}

	topLevel := flag.NewFlagSet("page-hub", flag.ContinueOnError)
	versionFlag := topLevel.Bool("version", false, "print the Page Hub version and compatible catalog schema")
	if err := topLevel.Parse(os.Args[1:]); err != nil {
		exitWithUsage()
	}
	if *versionFlag {
		printVersion()
		return
	}
	if topLevel.NArg() > 0 {
		exitWithUsage()
	}
	// Bare invocation serves, matching deployment units that only set the
	// environment.
	runServe(nil)
}

func exitWithUsage() {
	fmt.Fprintf(os.Stderr, `page-hub — private manager for Publications

Usage:
  page-hub serve                                run the manager (default configuration from the environment)
  page-hub migrate -catalog <path>              apply pending catalog migrations
  page-hub plan -declaration <file> [-out <file>]
                                                plan an explicit adoption declaration against storage
  page-hub commit -plan <file> -operation-id <uuid> [-catalog <path>]
                                                commit an approved plan into the catalog
  page-hub version                              print version and compatible catalog schema
`)
	os.Exit(2)
}

func printVersion() {
	fmt.Printf("page-hub %s\ncompatible catalog schema: %s\n", version, catalog.SupportedSchemaRange())
}

// runServe starts the manager. The runtime never migrates; it refuses an
// incompatible, partially applied, or unmigrated catalog schema.
func runServe(args []string) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "page-hub configuration error: %v\n", err)
		os.Exit(2)
	}
	cfg.Version = version

	lock, err := catalog.AcquireLock(cfg.CatalogPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "page-hub catalog error: %v\n", err)
		os.Exit(2)
	}
	defer lock.Release()

	store, err := catalog.OpenRuntime(cfg.CatalogPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "page-hub catalog error: %v\n", err)
		os.Exit(2)
	}
	defer store.Close()

	application, err := server.New(cfg, storage.NewS3Checker(cfg.Storage), store, nil)
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

// runMigrate applies pending catalog migrations. It refuses to run while the
// manager holds the catalog.
func runMigrate(args []string) {
	flags := flag.NewFlagSet("migrate", flag.ExitOnError)
	catalogPath := flags.String("catalog", os.Getenv("PAGE_HUB_CATALOG_PATH"), "path to the private SQLite catalog")
	if err := flags.Parse(args); err != nil {
		os.Exit(2)
	}
	if *catalogPath == "" {
		fmt.Fprintln(os.Stderr, "migrate: a catalog path is required (-catalog or PAGE_HUB_CATALOG_PATH)")
		os.Exit(2)
	}
	from, to, err := catalog.Migrate(*catalogPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "page-hub migrate: %v\n", err)
		os.Exit(1)
	}
	slog.Info("catalog schema current", "from", from, "to", to)
}

// runPlan builds a reviewable adoption plan from an explicit declaration.
// It reads storage and changes neither storage nor the catalog.
func runPlan(args []string) {
	flags := flag.NewFlagSet("plan", flag.ExitOnError)
	declarationPath := flags.String("declaration", "", "path to the adoption declaration JSON")
	outputPath := flags.String("out", "", "write the plan to this file instead of stdout")
	if err := flags.Parse(args); err != nil {
		os.Exit(2)
	}
	if *declarationPath == "" {
		fmt.Fprintln(os.Stderr, "plan: -declaration <file> is required")
		os.Exit(2)
	}
	declaration, err := readDeclaration(*declarationPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "page-hub plan: %v\n", err)
		os.Exit(2)
	}
	reader := storage.NewS3Reader(storageConfigFromEnv())
	plan, err := adoption.BuildPlan(context.Background(), reader, declaration)
	if err != nil {
		fmt.Fprintf(os.Stderr, "page-hub plan: %v\n", err)
		os.Exit(1)
	}
	encoded, err := adoption.EncodePlan(plan)
	if err != nil {
		fmt.Fprintf(os.Stderr, "page-hub plan: %v\n", err)
		os.Exit(1)
	}
	if err := writePlan(encoded, *outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "page-hub plan: %v\n", err)
		os.Exit(1)
	}
	slog.Info("adoption plan ready", "digest", plan.Digest)
}

// runCommit accepts an approved plan into the catalog. It refuses to run while
// the manager holds the catalog and rechecks the candidate immediately before
// the transaction.
func runCommit(args []string) {
	flags := flag.NewFlagSet("commit", flag.ExitOnError)
	planPath := flags.String("plan", "", "path to the approved plan JSON")
	operationID := flags.String("operation-id", "", "caller-provided durable operation ID (UUIDv4)")
	catalogPath := flags.String("catalog", os.Getenv("PAGE_HUB_CATALOG_PATH"), "path to the private SQLite catalog")
	if err := flags.Parse(args); err != nil {
		os.Exit(2)
	}
	if *planPath == "" || *operationID == "" || *catalogPath == "" {
		fmt.Fprintln(os.Stderr, "commit: -plan <file>, -operation-id <uuid>, and a catalog path (-catalog or PAGE_HUB_CATALOG_PATH) are required")
		os.Exit(2)
	}
	encoded, err := os.ReadFile(*planPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "page-hub commit: %v\n", err)
		os.Exit(2)
	}
	plan, err := adoption.DecodePlan(encoded)
	if err != nil {
		fmt.Fprintf(os.Stderr, "page-hub commit: %v\n", err)
		os.Exit(2)
	}
	reader := storage.NewS3Reader(storageConfigFromEnv())
	result, err := adoption.Commit(context.Background(), reader, plan, *operationID, *catalogPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "page-hub commit: %v\n", err)
		os.Exit(1)
	}
	output := map[string]any{"result": result.Result, "replayed": result.Replayed}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		fmt.Fprintf(os.Stderr, "page-hub commit: %v\n", err)
		os.Exit(1)
	}
}

func readDeclaration(path string) (adoption.Declaration, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return adoption.Declaration{}, err
	}
	var declaration adoption.Declaration
	if err := json.Unmarshal(encoded, &declaration); err != nil {
		return adoption.Declaration{}, fmt.Errorf("parse declaration %s: %w", path, err)
	}
	return declaration, nil
}

func writePlan(encoded []byte, outputPath string) error {
	if outputPath == "" {
		_, err := os.Stdout.Write(encoded)
		return err
	}
	return os.WriteFile(outputPath, encoded, 0o600)
}

func storageConfigFromEnv() storage.Config {
	return config.StorageFromEnv()
}
