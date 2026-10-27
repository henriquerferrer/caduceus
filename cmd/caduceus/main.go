// Caduceus catches the SMS your application sends in development and tests,
// the way Mailpit catches email.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/henriquerferrer/caduceus/internal/server"
	"github.com/henriquerferrer/caduceus/internal/store"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func defaultDataDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "caduceus")
	}
	return ".caduceus"
}

func main() {
	listen := flag.String("listen", env("CADUCEUS_LISTEN", "127.0.0.1:8026"), "address for the UI, API and provider endpoints")
	database := flag.String("database", env("CADUCEUS_DATABASE", ""), "SQLite file for messages; empty uses a temporary database deleted on exit")
	maxMessages := flag.Int("max-messages", 5000, "messages, and separately verifications, to keep before the oldest are dropped (0 = unlimited)")
	delay := flag.Duration("delivery-delay", time.Second, "pause before each simulated status change; negative disables")
	inbound := flag.String("inbound-webhook", env("CADUCEUS_INBOUND_WEBHOOK", ""), "URL that receives simulated inbound messages")
	allowedHosts := flag.String("allowed-hosts", env("CADUCEUS_ALLOWED_HOSTS", ""), "comma-separated host names, beyond localhost, 127.0.0.1 and ::1, that the UI, API and provider endpoints answer, e.g. a Docker Compose service name")
	dataDir := flag.String("data-dir", env("CADUCEUS_DATA_DIR", defaultDataDir()), "directory for the proxy CA certificate")
	flag.Parse()

	ca, err := server.LoadCA(*dataDir)
	if err != nil {
		slog.Error("could not load the proxy CA", "dir", *dataDir, "err", err)
		os.Exit(1)
	}

	st, err := store.Open(*database, *maxMessages)
	if err != nil {
		slog.Error("could not open the database", "path", *database, "err", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr: *listen,
		Handler: server.New(st, server.Options{
			DeliveryDelay:  *delay,
			InboundWebhook: *inbound,
			CA:             ca,
			AllowedHosts:   server.SplitHosts(*allowedHosts),
		}),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()

	slog.Info("caduceus listening", "addr", *listen, "ca", ca.Path)
	err = srv.ListenAndServe()
	st.Close()
	if !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server failed", "err", err)
		os.Exit(1)
	}
}
