// Package api implements the REST API (spec §12) on top of the oapi-codegen generated
// std-http router (internal/api/gen), plus auth, CSRF and security middleware (§14).
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/2shiraz/loxy/internal/api/gen"
	"github.com/2shiraz/loxy/internal/auth"
	"github.com/2shiraz/loxy/internal/certs"
	"github.com/2shiraz/loxy/internal/config"
	"github.com/2shiraz/loxy/internal/events"
	"github.com/2shiraz/loxy/internal/haproxy/validate"
	"github.com/2shiraz/loxy/internal/logrecv"
	"github.com/2shiraz/loxy/internal/metrics"
	"github.com/2shiraz/loxy/internal/model"
	"github.com/2shiraz/loxy/internal/notify"
	"github.com/2shiraz/loxy/internal/reconcile"
	"github.com/2shiraz/loxy/internal/secrets"
	"github.com/2shiraz/loxy/internal/state"
	"github.com/2shiraz/loxy/internal/store"
	"github.com/2shiraz/loxy/internal/store/db"
)

type Server struct {
	Cfg      *config.Config
	Store    *store.Store
	Box      *secrets.Box
	Snap     *state.Holder
	Rec      *reconcile.Reconciler
	Metrics  *metrics.Collector
	Issuer   *certs.Issuer
	Hub      *events.Hub
	Val      *validate.Validator
	Version  string
	CSRFKey  []byte
	Static   http.Handler
	Limiter  *auth.Limiter
	NodeInfo func() (version string, features []string)
	Drain    func(backendID, serverID int64)
	Notifier *notify.Notifier
	Logs     *logrecv.Receiver
}

var _ gen.ServerInterface = (*Server)(nil)

type ctxKey int

const userKey ctxKey = 1

type principal struct {
	User      db.User
	SessionID string
	TokenID   int64
}

func userFrom(r *http.Request) *principal {
	p, _ := r.Context().Value(userKey).(*principal)
	return p
}

// Handler returns the full HTTP handler: /api/v1 (generated routes + middleware) and the SPA.
func (s *Server) Handler() http.Handler {
	api := gen.HandlerWithOptions(s, gen.StdHTTPServerOptions{
		BaseURL: "/api/v1",
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeErr(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		},
	})
	mux := http.NewServeMux()
	mux.Handle("/api/", s.middleware(api))
	mux.HandleFunc("GET /healthz", s.Healthz)
	mux.HandleFunc("GET /metrics", s.promMetrics)
	if s.Static != nil {
		mux.Handle("/", s.Static)
	}
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// uPlot sets inline styles on its canvas wrappers, hence style-src 'unsafe-inline' (ADR 0002).
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

var publicPaths = map[string]bool{"/api/v1/healthz": true, "/api/v1/readyz": true, "/api/v1/auth/login": true}

func (s *Server) cookieName() string {
	if s.Cfg.Dev {
		return auth.DevCookieName
	}
	return auth.CookieName
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := int64(1 << 20)
		if strings.HasPrefix(r.URL.Path, "/api/v1/certificates") {
			limit = 5 << 20
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		if publicPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		if p, present := s.bearer(r); present {
			if p == nil {
				writeErr(w, 401, "unauthorized", "invalid or expired API token", nil)
				return
			}
			if !auth.Allows(p.User.Role, requiredRole(r)) {
				writeErr(w, 403, "forbidden", "token role does not allow this action", nil)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, p)))
			return
		}
		c, err := r.Cookie(s.cookieName())
		if err != nil {
			writeErr(w, 401, "unauthorized", "authentication required", nil)
			return
		}
		ctx := r.Context()
		sess, err := s.Store.Q.GetSession(ctx, auth.HashSessionID(c.Value))
		now := time.Now()
		if err != nil || now.UnixMilli() > sess.ExpiresAt || now.Sub(time.UnixMilli(sess.LastSeenAt)) > auth.IdleTimeout {
			writeErr(w, 401, "unauthorized", "session expired", nil)
			return
		}
		u, err := s.Store.Q.GetUser(ctx, sess.UserID)
		if err != nil || u.Disabled != 0 {
			writeErr(w, 401, "unauthorized", "user disabled", nil)
			return
		}
		if now.Sub(time.UnixMilli(sess.LastSeenAt)) > time.Minute {
			_ = s.Store.Q.TouchSession(ctx, db.TouchSessionParams{LastSeenAt: now.UnixMilli(), IDHash: sess.IDHash})
		}
		mutating := r.Method != http.MethodGet && r.Method != http.MethodHead
		if mutating && !auth.CheckCSRF(s.CSRFKey, c.Value, r.Header.Get("X-LOXY-CSRF")) {
			writeErr(w, 403, "forbidden", "missing or invalid CSRF token", nil)
			return
		}
		if !auth.Allows(u.Role, requiredRole(r)) {
			writeErr(w, 403, "forbidden", "your role does not allow this action", nil)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, userKey, &principal{User: u, SessionID: c.Value})))
	})
}

var adminPrefixes = []string{"/api/v1/users", "/api/v1/nodes", "/api/v1/api_tokens", "/api/v1/notification_channels", "/api/v1/alert_rules"}

// requiredRole maps a request to the minimum role (§14.2).
func requiredRole(r *http.Request) string {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		for _, p := range []string{"/api/v1/api_tokens", "/api/v1/notification_channels", "/api/v1/alert_rules", "/api/v1/users"} {
			if strings.HasPrefix(r.URL.Path, p) {
				return "admin"
			}
		}
		return "viewer"
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/auth/") { // own session, password, 2FA
		return "viewer"
	}
	for _, p := range adminPrefixes {
		if strings.HasPrefix(r.URL.Path, p) {
			return "admin"
		}
	}
	return "operator"
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type apiError struct {
	Status int
	Code   string
	Msg    string
	Fields model.FieldErrors
}

func (e *apiError) Error() string { return e.Msg }

func errInput(fe model.FieldErrors) *apiError {
	return &apiError{Status: 422, Code: "invalid_input", Msg: "invalid input: " + fe.Error(), Fields: fe}
}
func errField(field, msg string) *apiError {
	fe := model.FieldErrors{}
	fe.Add(field, msg)
	return errInput(fe)
}
func errNotFound(what string) *apiError {
	return &apiError{Status: 404, Code: "not_found", Msg: what + " not found"}
}
func errConflict(msg string) *apiError { return &apiError{Status: 409, Code: "conflict", Msg: msg} }

func writeErr(w http.ResponseWriter, status int, code, msg string, fields model.FieldErrors) {
	body := map[string]any{"code": code, "message": msg}
	if len(fields) > 0 {
		var fs []map[string]any
		for k, v := range fields {
			fs = append(fs, map[string]any{"name": k, "messages": v})
		}
		body["details"] = map[string]any{"fields": fs}
	}
	writeJSON(w, status, map[string]any{"error": body})
}

func fail(w http.ResponseWriter, err error) {
	var ae *apiError
	var mbe *http.MaxBytesError
	switch {
	case errors.As(err, &ae):
		writeErr(w, ae.Status, ae.Code, ae.Msg, ae.Fields)
	case errors.As(err, &mbe):
		writeErr(w, 413, "invalid_input", "request body too large", nil)
	case errors.Is(err, sql.ErrNoRows):
		writeErr(w, 404, "not_found", "not found", nil)
	case strings.Contains(err.Error(), "UNIQUE constraint failed"):
		writeErr(w, 409, "conflict", "already exists", nil)
	case strings.Contains(err.Error(), "FOREIGN KEY constraint failed"):
		writeErr(w, 409, "conflict", "resource is still in use", nil)
	default:
		slog.Error("api", "err", err)
		writeErr(w, 500, "internal", "internal error", nil)
	}
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return &apiError{Status: 400, Code: "invalid_input", Msg: "invalid JSON: " + err.Error()}
	}
	return nil
}

func clientIP(r *http.Request) string {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	// Only trust X-Forwarded-For from the local HAProxy sys_panel frontend.
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		// HAProxy (option forwardfor) appends the real peer as the LAST entry; earlier
		// entries are client-controlled and must not be trusted.
		if vals := r.Header.Values("X-Forwarded-For"); len(vals) > 0 {
			parts := strings.Split(vals[len(vals)-1], ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	return host
}

func nowMs() int64 { return time.Now().UnixMilli() }

func jsonStr(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// mutate runs fn in one transaction, resolves target slots, bumps the node's desired version,
// writes an audit row, refreshes the snapshot and triggers the reconciler (§3.3).
func (s *Server) mutate(r *http.Request, action, resType string, resID func() int64, after any, fn func(q *db.Queries) error) error {
	ctx := r.Context()
	err := s.Store.Tx(ctx, func(q *db.Queries) error {
		if err := fn(q); err != nil {
			return err
		}
		if err := state.SyncSlots(ctx, q); err != nil {
			return err
		}
		if _, err := q.BumpDesiredVersion(ctx, 1); err != nil {
			return err
		}
		uid, tid, actor := actorOf(r)
		var rid sql.NullInt64
		if resID != nil {
			rid = sql.NullInt64{Int64: resID(), Valid: true}
		}
		var afterJ sql.NullString
		if after != nil {
			afterJ = sql.NullString{String: jsonStr(after), Valid: true}
		}
		return q.CreateAudit(ctx, db.CreateAuditParams{Ts: nowMs(), ActorUserID: uid, ActorTokenID: tid, Actor: actor, Ip: clientIP(r), Action: action,
			ResourceType: resType, ResourceID: rid, After: afterJ})
	})
	if err != nil {
		return err
	}
	s.Changed(ctx)
	return nil
}

// Changed refreshes the snapshot and kicks the reconciler.
func (s *Server) Changed(ctx context.Context) {
	if _, err := s.Snap.Refresh(ctx, s.Store.Q, s.Box); err != nil {
		slog.Error("refresh snapshot", "err", err)
	}
	s.Rec.Trigger()
	s.Hub.Publish(events.Event{Kind: "config.updated", Ephemeral: true})
}

// checkIfMatch enforces optimistic concurrency: a stale If-Match version yields 409.
func checkIfMatch(r *http.Request, version int64) error {
	if m := r.Header.Get("If-Match"); m != "" && strings.Trim(m, `"W/`) != fmt.Sprint(version) {
		return errConflict("resource was modified (version mismatch)")
	}
	return nil
}

func ptr[T any](v T) *T { return &v }

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullI64(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func i64(v *int64) sql.NullInt64 {
	if v == nil || *v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

func actorOf(r *http.Request) (uid, tid sql.NullInt64, actor string) {
	p := userFrom(r)
	if p == nil {
		return
	}
	actor = p.User.Email
	if p.TokenID != 0 {
		tid = sql.NullInt64{Int64: p.TokenID, Valid: true}
	} else {
		uid = sql.NullInt64{Int64: p.User.ID, Valid: true}
	}
	return
}

// promMetrics exposes controller metrics (loopback only; never exposed through sys_panel).
func (s *Server) promMetrics(w http.ResponseWriter, r *http.Request) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() || r.Header.Get("X-Forwarded-For") != "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	reconcile.Stats.WriteProm(w)
	snap := s.Snap.Get()
	_, _ = w.Write([]byte("# TYPE loxy_frontends gauge\nloxy_frontends " + strconv.Itoa(len(snap.Frontends)) + "\n# TYPE loxy_backends gauge\nloxy_backends " + strconv.Itoa(len(snap.Backends)) + "\n"))
	if s.Metrics != nil {
		_, reloads, last := s.Metrics.Info()
		_, _ = w.Write([]byte("# TYPE loxy_haproxy_worker_restarts_total counter\nloxy_haproxy_worker_restarts_total " + strconv.Itoa(reloads) + "\n"))
		_, _ = w.Write([]byte("# TYPE loxy_stats_last_poll_timestamp_seconds gauge\nloxy_stats_last_poll_timestamp_seconds " + strconv.FormatInt(last.Unix(), 10) + "\n"))
	}
	for _, c := range snap.Certs {
		if c.NotAfter.Valid {
			_, _ = w.Write([]byte("loxy_certificate_not_after_seconds{id=\"" + strconv.FormatInt(c.ID, 10) + "\"} " + strconv.FormatInt(c.NotAfter.Int64/1000, 10) + "\n"))
		}
	}
}
