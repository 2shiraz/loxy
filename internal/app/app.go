// Package app wires all components of the all-in-one process and manages its lifecycle.
package app

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-acme/lego/v5/challenge"

	"github.com/2shiraz/loxy/internal/agent"
	"github.com/2shiraz/loxy/internal/api"
	"github.com/2shiraz/loxy/internal/auth"
	"github.com/2shiraz/loxy/internal/certs"
	"github.com/2shiraz/loxy/internal/config"
	"github.com/2shiraz/loxy/internal/dns"
	"github.com/2shiraz/loxy/internal/events"
	"github.com/2shiraz/loxy/internal/haproxy/revfs"
	"github.com/2shiraz/loxy/internal/haproxy/runtime"
	"github.com/2shiraz/loxy/internal/haproxy/validate"
	"github.com/2shiraz/loxy/internal/logrecv"
	"github.com/2shiraz/loxy/internal/metrics"
	"github.com/2shiraz/loxy/internal/notify"
	"github.com/2shiraz/loxy/internal/reconcile"
	"github.com/2shiraz/loxy/internal/secrets"
	"github.com/2shiraz/loxy/internal/state"
	"github.com/2shiraz/loxy/internal/store"
	"github.com/2shiraz/loxy/internal/store/db"
	"github.com/2shiraz/loxy/internal/webui"
)

type App struct {
	Cfg     *config.Config
	Store   *store.Store
	Box     *secrets.Box
	Hub     *events.Hub
	Snap    *state.Holder
	Rec     *reconcile.Reconciler
	Metrics *metrics.Collector
	Issuer  *certs.Issuer
	API     *api.Server
	Val     *validate.Validator

	haproxyVersion string
	features       []string
}

// SecretKey returns LOXY_SECRET_KEY or a key persisted in the data dir (generated once).
func SecretKey(cfg *config.Config) ([]byte, error) {
	if cfg.SecretKey != nil {
		return cfg.SecretKey, nil
	}
	p := filepath.Join(cfg.DataDir, "secret.key")
	if b, err := os.ReadFile(p); err == nil {
		k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
		if err == nil && len(k) == 32 {
			return k, nil
		}
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, []byte(base64.StdEncoding.EncodeToString(k)+"\n"), 0o600); err != nil {
		return nil, err
	}
	slog.Warn("LOXY_SECRET_KEY not set; generated one in the data dir. Back it up together with loxy.db", "file", p)
	return k, nil
}

func derive(key []byte, label string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(label))
	return m.Sum(nil)
}

// New opens the database and builds all components (no goroutines started).
func New(cfg *config.Config, version string) (*App, error) {
	for _, d := range []string{cfg.DataDir, cfg.HAProxyConfDir} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return nil, err
		}
	}
	key, err := SecretKey(cfg)
	if err != nil {
		return nil, err
	}
	box, err := secrets.New(key)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "loxy.db"))
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	if err := st.Seed(ctx); err != nil {
		return nil, err
	}
	a := &App{Cfg: cfg, Store: st, Box: box, Hub: events.NewHub(st.Q), Snap: &state.Holder{}, Val: validate.New(cfg.HAProxyBin)}
	if err := a.ensureOwner(ctx); err != nil {
		return nil, err
	}
	panelPEM, err := a.selfSigned(ctx, "panel_pem", "loxy-panel", cfg.PanelDomain)
	if err != nil {
		return nil, err
	}
	placeholder, err := a.selfSigned(ctx, "placeholder_pem", "loxy-placeholder", "")
	if err != nil {
		return nil, err
	}
	if v, f, err := a.Val.Version(ctx); err == nil {
		a.haproxyVersion, a.features = v, f
		fj, _ := json.Marshal(f)
		_ = st.Q.UpdateNodeInfo(ctx, db.UpdateNodeInfoParams{Status: "online", HaproxyVersion: v, LoxyVersion: version, Features: string(fj),
			LastSeenAt: sqlNow(), ID: 1})
	} else {
		slog.Warn("cannot run haproxy -vv; config validation will fail until it is available", "bin", cfg.HAProxyBin, "err", err)
	}
	rt := runtime.New(filepath.Join(cfg.HAProxyRunDir, "admin.sock"), filepath.Join(cfg.HAProxyRunDir, "master.sock"))
	node := &agent.Local{RT: rt}
	maxconn := 200000
	if v := os.Getenv("LOXY_HAPROXY_MAXCONN"); v != "" {
		fmt.Sscan(v, &maxconn)
	}
	a.Rec = reconcile.New(st, box, revfs.New(cfg.HAProxyConfDir), a.Val, node, a.Hub, reconcile.Options{
		NodeID: 1, PanelPort: cfg.PanelPort, PanelAllow: cfg.PanelAllow, LoxyAddr: cfg.Listen, StateDir: cfg.HAProxyStateDir,
		SocketPath: filepath.Join(cfg.HAProxyRunDir, "admin.sock"), GlobalMaxConn: maxconn,
		PanelPEM: panelPEM, PlaceholderPEM: placeholder, NoHAProxy: cfg.NoHAProxy,
	})
	for _, f := range a.features {
		if f == "PROMEX" {
			a.Rec.SetPromex(true)
		}
	}
	a.Metrics = metrics.New(node, st.Q, a.Hub, cfg.StatsInterval)
	if _, err := a.Snap.Refresh(ctx, st.Q, box); err != nil {
		return nil, err
	}
	a.API = &api.Server{Cfg: cfg, Store: st, Box: box, Snap: a.Snap, Rec: a.Rec, Metrics: a.Metrics, Hub: a.Hub, Val: a.Val, Version: version,
		CSRFKey: derive(key, "csrf"), Static: webui.Handler(), Limiter: auth.NewLimiter(),
		NodeInfo: func() (string, []string) { return a.haproxyVersion, a.features }}
	a.Issuer = &certs.Issuer{Q: st.Q, Box: box, Directory: cfg.ACMEDirectory, Email: cfg.ACMEEmail, Insecure: cfg.ACMEInsecure, HTTP: a.Rec,
		DNS: func(ctx context.Context, id int64) (challenge.Provider, error) {
			p, err := a.API.DNSProvider(ctx, id)
			if err != nil {
				return nil, err
			}
			return dns.NewACMEProvider(p), nil
		},
		OnChange: func(id int64) {
			c := context.Background()
			_, _ = st.Q.BumpDesiredVersion(c, 1)
			a.API.Changed(c)
			a.Hub.Publish(events.Event{Kind: "certificate.updated", Message: fmt.Sprintf("Certificate %d updated", id), Ephemeral: true, Data: map[string]any{"certificate_id": id}})
		}}
	a.API.Issuer = a.Issuer
	a.Issuer.Ready = func(id int64) bool {
		snap := a.Snap.Get()
		n := snap.Node(1)
		if n == nil || n.AppliedVersion < n.DesiredVersion {
			return false
		}
		for _, lb := range snap.LBs {
			for _, sv := range lb.Services {
				for _, c := range sv.CertIDs {
					if c == id {
						return true
					}
				}
			}
		}
		for _, d := range snap.Domains {
			if d.CertificateID.Valid && d.CertificateID.Int64 == id && d.LbID.Valid {
				return true
			}
		}
		return false
	}
	if cfg.LiveLogs > 0 {
		a.API.Logs = logrecv.New("127.0.0.1:5514", cfg.LiveLogs)
	}
	a.Rec.OnApplied = func() {
		_, _ = a.Snap.Refresh(context.Background(), st.Q, box)
		a.Issuer.Kick()
	}
	a.API.Notifier = notify.New(st.Q, box, a.Hub, func(id int64) map[string]string {
		if lb := a.Snap.Get().LB(id); lb != nil {
			return lb.Labels
		}
		return nil
	})
	a.API.Drain = a.drain
	return a, nil
}

func sqlNow() sql.NullInt64 { return sql.NullInt64{Int64: time.Now().UnixMilli(), Valid: true} }

func (a *App) ensureOwner(ctx context.Context) error {
	n, err := a.Store.Q.CountUsers(ctx)
	if err != nil || n > 0 {
		return err
	}
	email := os.Getenv("LOXY_OWNER_EMAIL")
	if email == "" {
		email = "admin@loxy.local"
	}
	pw := os.Getenv("LOXY_OWNER_PASSWORD")
	if len(pw) < auth.MinPassword {
		pw = auth.RandomPassword()
	}
	h, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	if _, err := a.Store.Q.CreateUser(ctx, db.CreateUserParams{Email: email, PasswordHash: h, Role: "owner", CreatedAt: time.Now().UnixMilli()}); err != nil {
		return err
	}
	slog.Warn("created owner account — initial password (shown once)", "email", email, "initial_password", pw)
	return nil
}

// ResetOwnerPassword sets a new random owner password and returns it.
func (a *App) ResetOwnerPassword(ctx context.Context) (string, string, error) {
	u, err := a.Store.Q.GetOwner(ctx)
	if err != nil {
		return "", "", err
	}
	pw := auth.RandomPassword()
	h, err := auth.HashPassword(pw)
	if err != nil {
		return "", "", err
	}
	if err := a.Store.Q.SetUserPassword(ctx, db.SetUserPasswordParams{PasswordHash: h, ID: u.ID}); err != nil {
		return "", "", err
	}
	_ = a.Store.Q.DeleteUserSessions(ctx, u.ID)
	return u.Email, pw, nil
}

// selfSigned loads or creates an encrypted self-signed PEM stored in settings.
func (a *App) selfSigned(ctx context.Context, key, cn, dnsName string) (string, error) {
	if v, err := a.Store.Q.GetSetting(ctx, key); err == nil {
		var enc []byte
		if json.Unmarshal([]byte(v), &enc) == nil {
			if pem, err := a.Box.Open(enc); err == nil {
				return string(pem), nil
			}
		}
	}
	var names []string
	if dnsName != "" {
		names = []string{dnsName}
	}
	c, k, err := certs.SelfSigned(cn, names, 10*365*24*time.Hour)
	if err != nil {
		return "", err
	}
	pem := c + k
	enc, _ := json.Marshal(a.Box.Seal([]byte(pem)))
	if err := a.Store.Q.PutSetting(ctx, db.PutSettingParams{Key: key, Value: string(enc)}); err != nil {
		return "", err
	}
	return pem, nil
}

// drain gracefully removes a target: it is already in drain state; wait for open
// connections to reach zero (or the timeout), then delete it (§7.6).
func (a *App) drain(lbID, targetID int64) {
	timeout := 30 * time.Second
	if v := os.Getenv("LOXY_DRAIN_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			timeout = d
		}
	}
	deadline := time.Now().Add(timeout)
	time.Sleep(1500 * time.Millisecond) // let the drain state reach HAProxy
	for time.Now().Before(deadline) && !a.Cfg.NoHAProxy {
		lb := a.Snap.Get().LB(lbID)
		if lb == nil || lb.Target(targetID) == nil {
			return
		}
		busy := false
		for _, ch := range a.Metrics.Checks(lbID) {
			for _, sl := range lb.Target(targetID).Slots {
				if ch.SlotID == sl.ID && ch.Scur > 0 {
					busy = true
				}
			}
		}
		if !busy {
			break
		}
		time.Sleep(time.Second)
	}
	ctx := context.Background()
	err := a.Store.Tx(ctx, func(q *db.Queries) error {
		if err := q.DeleteTarget(ctx, targetID); err != nil {
			return err
		}
		if err := state.SyncSlots(ctx, q); err != nil {
			return err
		}
		_, err := q.BumpDesiredVersion(ctx, 1)
		return err
	})
	if err != nil {
		slog.Warn("remove target", "err", err)
		return
	}
	a.API.Changed(ctx)
}

// Run starts all loops and the HTTP server; it blocks until ctx is cancelled.
func (a *App) Run(ctx context.Context) error {
	if err := a.Rec.Apply(ctx, "startup"); err != nil {
		slog.Error("initial apply failed (previous live config stays)", "err", err)
	}
	go a.Rec.Run(ctx)
	if !a.Cfg.NoHAProxy {
		go a.Metrics.Run(ctx)
	}
	go a.Issuer.RunScheduler(ctx)
	go a.API.RunDomainLoop(ctx)
	go a.API.Notifier.Run(ctx)
	go a.runBackups(ctx)
	if a.API.Logs != nil {
		go a.API.Logs.Run(ctx)
	}
	srv := &http.Server{Addr: a.Cfg.Listen, Handler: a.API.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", a.Cfg.Listen)
		errc <- srv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
		return a.Store.Close()
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Backup writes an online copy of the database (VACUUM INTO) and keeps the newest `keep`.
func Backup(st *store.Store, dir string, keep int) (string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, "loxy-"+time.Now().UTC().Format("20060102-150405")+".db")
	if _, err := st.DB.Exec(`VACUUM INTO ?`, dst); err != nil {
		return "", err
	}
	ents, _ := filepath.Glob(filepath.Join(dir, "loxy-*.db"))
	for i := 0; i < len(ents)-keep; i++ {
		_ = os.Remove(ents[i])
	}
	return dst, nil
}

// runBackups takes a nightly backup (§16.6), keeping 14.
func (a *App) runBackups(ctx context.Context) {
	for {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), 3, 17, 0, 0, time.Local)
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
		if p, err := Backup(a.Store, filepath.Join(a.Cfg.DataDir, "backups"), 14); err != nil {
			slog.Error("nightly backup failed", "err", err)
			a.Hub.Publish(events.Event{Kind: "backup.failed", Severity: "error", Message: err.Error()})
		} else {
			slog.Info("nightly backup", "file", p)
		}
	}
}
