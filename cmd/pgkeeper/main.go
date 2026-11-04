package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/app"
	"github.com/banshee-lab/pgkeeper/internal/config"
	"github.com/banshee-lab/pgkeeper/internal/domain"
	"github.com/banshee-lab/pgkeeper/internal/notify"
	"github.com/banshee-lab/pgkeeper/internal/reload"
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
	checkRestic := flag.Bool("check-restic", false, "run 'restic cat config' for every distinct repository and exit")
	reloadFlag := flag.Bool("reload", false, "validate the config, then ask the running pgkeeper to reload it (SIGHUP) and exit")
	notifyTest := flag.Bool("notify-test", false, "send a test Telegram message and exit")
	runName := flag.String("run", "", "run one backup immediately and exit (0 = success)")
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
	if *reloadFlag {
		// The config was validated by config.Load above; an invalid file never
		// reaches the daemon.
		pid, err := reload.Signal(reload.PIDFile(cfg.Global.TempPath))
		if err != nil {
			return err
		}
		fmt.Printf("config is valid; reload requested from pgkeeper (pid %d), see its log for the result\n", pid)
		return nil
	}
	if *checkRestic {
		cctx, cstop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cstop()
		return app.CheckRestic(cctx, cfg, os.Stdout)
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return app.Run(ctx, cfg, *configPath, notifier, log, *runName)
}