// Command loxy is the Loxy control plane: a managed load balancer panel on HAProxy 3.4.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/2shiraz/loxy/internal/app"
	"github.com/2shiraz/loxy/internal/config"
	"github.com/2shiraz/loxy/internal/store"
)

var version = "dev"

const usage = `loxy — managed load balancer panel on HAProxy

Usage:
  loxy all-in-one              run panel + local node agent (default)
  loxy render --dry-run        print the HAProxy config that would be applied
  loxy migrate                 run database migrations and exit
  loxy backup [file]           online backup of loxy.db (VACUUM INTO)
  loxy restore <file>          restore loxy.db from a backup (stop loxy first)
  loxy admin reset-password    set a new random owner password
  loxy version                 print the version
  loxy api METHOD PATH [JSON]  call the REST API (env LOXY_URL, LOXY_TOKEN)
  loxy lb ls                   list load balancers (env LOXY_URL, LOXY_TOKEN)
  loxy panel | agent           multi-node modes (Phase 3, not available yet)
`

func main() {
	cmd := "all-in-one"
	args := os.Args[1:]
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	if err := run(cmd, args); err != nil {
		fmt.Fprintln(os.Stderr, "loxy:", err)
		os.Exit(1)
	}
}

func setupLog(level string) {
	var l slog.Level
	_ = l.UnmarshalText([]byte(level))
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l})))
}

func run(cmd string, args []string) error {
	switch cmd {
	case "version", "--version", "-v":
		fmt.Println(version)
		return nil
	case "help", "--help", "-h":
		fmt.Print(usage)
		return nil
	case "api":
		return runAPI(args)
	case "lb":
		return runLB(args)
	case "panel", "agent":
		return fmt.Errorf("%s mode is part of Phase 3 and not implemented yet; use all-in-one", cmd)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	setupLog(cfg.LogLevel)
	switch cmd {
	case "all-in-one":
		if os.Getenv("GOMEMLIMIT") == "" {
			// Soft heap limit keeps RSS inside the §1.4 budget; GC works harder only near it.
			debug.SetMemoryLimit(48 << 20)
		}
		if addr := os.Getenv("LOXY_PPROF"); addr != "" {
			// Opt-in profiling listener (loopback only), for performance work.
			go func() { _ = http.ListenAndServe(addr, nil) }() //nolint:gosec
		}
		a, err := app.New(cfg, version)
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		return a.Run(ctx)
	case "render":
		cfg.NoHAProxy = true
		a, err := app.New(cfg, version)
		if err != nil {
			return err
		}
		out, err := a.Rec.RenderLive(context.Background())
		if err != nil {
			return err
		}
		fmt.Print(out.Config())
		return nil
	case "migrate":
		s, err := store.Open(filepath.Join(cfg.DataDir, "loxy.db"))
		if err != nil {
			return err
		}
		defer s.Close()
		return s.Seed(context.Background())
	case "backup":
		s, err := store.Open(filepath.Join(cfg.DataDir, "loxy.db"))
		if err != nil {
			return err
		}
		defer s.Close()
		dst := filepath.Join(cfg.DataDir, "backups", "loxy-"+time.Now().UTC().Format("20060102-150405")+".db")
		if len(args) > 0 {
			dst = args[0]
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return err
		}
		if _, err := s.DB.Exec(`VACUUM INTO ?`, dst); err != nil {
			return err
		}
		fmt.Println(dst)
		return nil
	case "restore":
		if len(args) != 1 {
			return fmt.Errorf("usage: loxy restore <file>")
		}
		src, err := os.Open(args[0])
		if err != nil {
			return err
		}
		defer src.Close()
		dbPath := filepath.Join(cfg.DataDir, "loxy.db")
		_ = os.Remove(dbPath + "-wal")
		_ = os.Remove(dbPath + "-shm")
		dst, err := os.OpenFile(dbPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
		if err != nil {
			return err
		}
		defer dst.Close()
		_, err = io.Copy(dst, src)
		return err
	case "admin":
		if len(args) == 0 || (args[0] != "reset-password" && args[0] != "seed") {
			return fmt.Errorf("usage: loxy admin reset-password | loxy admin seed [target-ip]")
		}
		cfg.NoHAProxy = true
		a, err := app.New(cfg, version)
		if err != nil {
			return err
		}
		if args[0] == "seed" {
			ip := "127.0.0.1"
			if len(args) > 1 {
				ip = args[1]
			}
			if err := a.SeedDemo(context.Background(), ip); err != nil {
				return err
			}
			fmt.Println("demo data created; a running loxy applies it on its next reconcile (or restart)")
			return nil
		}
		email, pw, err := a.ResetOwnerPassword(context.Background())
		if err != nil {
			return err
		}
		fmt.Printf("owner %s: new password %s\n", email, pw)
		return nil
	}
	fmt.Print(usage)
	return fmt.Errorf("unknown command %q", cmd)
}
