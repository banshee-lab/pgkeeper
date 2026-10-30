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

	"github.com/banshee-lab/pgkeeper/internal/api"
	"github.com/banshee-lab/pgkeeper/internal/config"
	"github.com/banshee-lab/pgkeeper/internal/domain"
	"github.com/banshee-lab/pgkeeper/internal/notify"
	"github.com/banshee-lab/pgkeeper/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func run() error {
	configPath := flag.String("config", envOr("PGKEEPER_CONFIG", "/etc/pgkeeper/pgkeeper.toml"), "path to the TOML config")
	check := flag.Bool("check", false, "validate the config, print a summary and exit")
	notifyTest := flag.Bool("notify-test", false, "send a test Telegram message and exit")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)

	cfg, err := config.Load(*configPath)
	if err != nil {
		// all validation errors, one per line
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, w := range cfg.Warnings {
		log.Warn(w)
	}
	if *check {
		fmt.Print(cfg.LogSafe())
		return nil
	}
	log.Info("config loaded", "summary", cfg.LogSafe())

	var notifier domain.Notifier = notify.Noop{}
	var tg *notify.Telegram
	if cfg.Telegram != nil {
		tg = notify.NewTelegram(*cfg.Telegram, cfg.Global.Location, log)
		notifier = tg
	}
	if *notifyTest {
		if tg == nil {
			return errors.New("no [telegram] section configured")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		msg := "✅ pgkeeper notify-test " + time.Now().In(cfg.Global.Location).Format("2006-01-02 15:04:05 -07")
		if err := tg.Send(ctx, msg); err != nil {
			return err
		}
		fmt.Println("test message sent")
		return nil
	}
	_ = notifier // consumed by the runner in phase 3

	db, err := store.Open(cfg.Global.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := store.Migrate(db); err != nil {
		return err
	}

	h := api.NewHandler(api.Deps{
		DB:         db,
		Activities: store.NewActivityStore(db),
		Sessions:   store.NewSessionStore(db),
		Logger:     log,
		// Catalog: cfg, // add once api.Deps has a catalog field (see note below)
	})
	srv := api.NewServer(cfg.Global.Listen, api.NewRouter(h))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	shCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return srv.Shutdown(shCtx)
}