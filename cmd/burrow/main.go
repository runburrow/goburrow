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

	"github.com/runburrow/goburrow/internal/ingest"
	"github.com/runburrow/goburrow/internal/mail"
	"github.com/runburrow/goburrow/internal/notify"
	"github.com/runburrow/goburrow/internal/store"
	"github.com/runburrow/goburrow/internal/web"
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
		rateLimit = flag.Int("rate-limit", atoi(env("BURROW_RATE_LIMIT", "3000")), "events per minute each project may send, 0 = no limit (BURROW_RATE_LIMIT)")
		proxy     = flag.Bool("behind-proxy", env("BURROW_BEHIND_PROXY", "") == "true", "trust the last X-Forwarded-For entry as the client address, for login throttling behind a reverse proxy (BURROW_BEHIND_PROXY=true)")
		whPrivate = flag.Bool("webhook-allow-private", env("BURROW_WEBHOOK_ALLOW_PRIVATE", "") == "true", "allow webhooks on private/LAN addresses, which are refused by default (BURROW_WEBHOOK_ALLOW_PRIVATE=true)")
		smtpHost  = flag.String("smtp-host", env("BURROW_SMTP_HOST", ""), "SMTP server for email alerts and invites; email is off when empty (BURROW_SMTP_HOST)")
		smtpPort  = flag.Int("smtp-port", atoi(env("BURROW_SMTP_PORT", "587")), "SMTP port (BURROW_SMTP_PORT)")
		smtpUser  = flag.String("smtp-username", env("BURROW_SMTP_USERNAME", ""), "SMTP username (BURROW_SMTP_USERNAME)")
		smtpFrom  = flag.String("smtp-from", env("BURROW_SMTP_FROM", ""), "address emails come from, e.g. \"Burrow <burrow@example.com>\" (BURROW_SMTP_FROM)")
		smtpTLS   = flag.String("smtp-tls", env("BURROW_SMTP_TLS", ""), "tls (port 465), starttls (587, the default) or none for a relay on this machine (BURROW_SMTP_TLS)")
		showVer   = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()
	if *showVer {
		fmt.Println("burrow", version)
		return
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log, config{addr: *addr, dbPath: *dbPath, baseURL: *baseURL, retentionDays: *retention, rateLimit: *rateLimit,
		webhookURL: *webhook, webhookAllowPrivate: *whPrivate, behindProxy: *proxy,
		// The password only comes from the environment: flags show up in
		// process listings.
		smtp: mail.Config{Host: *smtpHost, Port: *smtpPort, Username: *smtpUser, Password: os.Getenv("BURROW_SMTP_PASSWORD"), From: *smtpFrom, TLS: *smtpTLS},
	}); err != nil {
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

// config is the server's settings, from flags or environment variables.
type config struct {
	addr, dbPath, baseURL string
	retentionDays         int
	rateLimit             int // events per minute per project; 0 = off
	webhookURL            string
	webhookAllowPrivate   bool
	behindProxy           bool
	smtp                  mail.Config // Host empty: no email
}

func run(log *slog.Logger, c config) error {
	addr, base := c.addr, c.baseURL
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

	st, err := store.Open(c.dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	// Per-project webhooks live in the database; webhookURL is the fallback
	// for projects that have none.
	if c.webhookURL != "" {
		if err := notify.CheckURL(c.webhookURL, c.webhookAllowPrivate); err != nil {
			return fmt.Errorf("webhook URL: %w (set BURROW_WEBHOOK_ALLOW_PRIVATE=true for a LAN address)", err)
		}
	}
	alerts := notify.NewDispatcher(st, c.webhookURL, bu.String(), c.webhookAllowPrivate, log)
	if c.smtp.Host != "" {
		m, err := mail.New(c.smtp)
		if err != nil {
			return fmt.Errorf("SMTP: %w", err)
		}
		alerts.Mail = m
		// Check the connection once in the background: a bad setup is
		// logged at startup, not discovered with the first alert.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := m.Check(ctx); err != nil {
				_, why := mail.ErrorReason(err)
				log.Warn("SMTP check failed; email alerts and invites won't send", "server", m.Describe(), "reason", why, "err", err)
				return
			}
			log.Info("SMTP ready", "server", m.Describe())
		}()
	}
	ing := &ingest.Handler{Store: st, Log: log, Notifier: alerts, Limit: ingest.NewRateLimiter(c.rateLimit)}
	srv := &web.Server{Store: st, Ingest: ing, Notify: alerts, Log: log, BaseURL: bu, Version: version, BehindProxy: c.behindProxy}
	h, err := srv.Handler()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go maintenance(ctx, log, st, c.retentionDays)

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
	log.Info("burrow listening", "addr", addr, "base_url", bu.String(), "db", c.dbPath, "version", version)

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
