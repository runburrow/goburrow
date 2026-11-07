// Command burrow runs the Burrow error-tracking server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mozthewah/opensourcedsentryalternative/internal/ingest"
	"github.com/mozthewah/opensourcedsentryalternative/internal/notify"
	"github.com/mozthewah/opensourcedsentryalternative/internal/store"
	"github.com/mozthewah/opensourcedsentryalternative/internal/web"
)

// version is set at build time: -ldflags "-X main.version=v0.1.0"
var version = "dev"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	var (
		addr      = flag.String("addr", env("BURROW_ADDR", ":8000"), "listen address (BURROW_ADDR)")
		dbPath    = flag.String("db", env("BURROW_DB", "burrow.db"), "SQLite database path (BURROW_DB)")
		baseURL   = flag.String("base-url", env("BURROW_BASE_URL", ""), "public URL, used in DSNs and links (BURROW_BASE_URL); defaults to http://localhost<addr>")
		retention = flag.Int("retention-days", atoi(env("BURROW_RETENTION_DAYS", "30")), "delete events older than this many days, 0 = keep forever (BURROW_RETENTION_DAYS)")
		webhook   = flag.String("webhook-url", env("BURROW_WEBHOOK_URL", ""), "fallback webhook for projects with none set in their settings: Slack, Discord or custom (BURROW_WEBHOOK_URL)")
		whPrivate = flag.Bool("webhook-allow-private", env("BURROW_WEBHOOK_ALLOW_PRIVATE", "") == "true", "allow webhooks on private/LAN addresses, which are refused by default (BURROW_WEBHOOK_ALLOW_PRIVATE=true)")
		showVer   = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()
	if *showVer {
		fmt.Println("burrow", version)
		return
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log, *addr, *dbPath, *baseURL, *retention, *webhook, *whPrivate); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func run(log *slog.Logger, addr, dbPath, base string, retentionDays int, webhookURL string, webhookAllowPrivate bool) error {
	if base == "" {
		host := addr
		if strings.HasPrefix(host, ":") {
			host = "localhost" + host
		}
		base = "http://" + host
	}
	bu, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || bu.Scheme == "" || bu.Host == "" {
		return fmt.Errorf("invalid base URL %q: want e.g. https://burrow.example.com", base)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	// Per-project webhooks live in the database; webhookURL is the fallback
	// for projects that have none.
	if webhookURL != "" {
		if err := notify.CheckURL(webhookURL, webhookAllowPrivate); err != nil {
			return fmt.Errorf("webhook URL: %w (set BURROW_WEBHOOK_ALLOW_PRIVATE=true for a LAN address)", err)
		}
	}
	alerts := notify.NewDispatcher(st, webhookURL, bu.String(), webhookAllowPrivate, log)
	ing := &ingest.Handler{Store: st, Log: log, Notifier: alerts}
	srv := &web.Server{Store: st, Ingest: ing, Notify: alerts, Log: log, BaseURL: bu, Version: version}
	h, err := srv.Handler()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go maintenance(ctx, log, st, retentionDays)

	hs := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	log.Info("burrow listening", "addr", addr, "base_url", bu.String(), "db", dbPath, "version", version)

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down")
		sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return hs.Shutdown(sctx)
	}
	return nil
}

// maintenance prunes old events and expired sessions every hour.
func maintenance(ctx context.Context, log *slog.Logger, st *store.Store, retentionDays int) {
	tick := func() {
		if retentionDays > 0 {
			n, err := st.Prune(ctx, time.Now().AddDate(0, 0, -retentionDays))
			if err != nil {
				log.Error("prune events", "err", err)
			} else if n > 0 {
				log.Info("pruned old events", "count", n)
			}
		}
		if err := st.PruneSessions(ctx); err != nil {
			log.Error("prune sessions", "err", err)
		}
	}
	tick()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
		}
	}
}
