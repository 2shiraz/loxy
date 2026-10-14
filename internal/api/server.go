// Package api implements the REST API (spec §12) on top of the oapi-codegen generated
// std-http router (internal/api/gen), plus auth, CSRF and security middleware (§14).
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/2shiraz/loxy/internal/api/gen"
	"github.com/2shiraz/loxy/internal/auth"
	"github.com/2shiraz/loxy/internal/certs"
	"github.com/2shiraz/loxy/internal/config"
	"github.com/2shiraz/loxy/internal/events"
	"github.com/2shiraz/loxy/internal/haproxy/validate"
	"github.com/2shiraz/loxy/internal/metrics"
	"github.com/2shiraz/loxy/internal/model"
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
	Drain    func(lbID, targetID int64)
}

var _ gen.ServerInterface = (*Server)(nil)

type ctxKey int

const userKey ctxKey = 1

type principal struct {
	User      db.User
	SessionID string
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
		need := "viewer"
		if mutating {
			need = "operator"
			if strings.HasPrefix(r.URL.Path, "/api/v1/users") || strings.HasPrefix(r.URL.Path, "/api/v1/nodes") {
				need = "admin"
			}
			if r.URL.Path == "/api/v1/auth/logout" || r.URL.Path == "/api/v1/auth/password" {
				need = "viewer"
			}
		}
		if !auth.Allows(u.Role, need) {
			writeErr(w, 403, "forbidden", "your role does not allow this action", nil)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, userKey, &principal{User: u, SessionID: c.Value})))
	})
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
func errQuota(msg string) *apiError    { return &apiError{Status: 422, Code: "quota_exceeded", Msg: msg} }
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
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
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
		p := userFrom(r)
		var uid sql.NullInt64
		actor := ""
		if p != nil {
			uid = sql.NullInt64{Int64: p.User.ID, Valid: true}
			actor = p.User.Email
		}
		var rid sql.NullInt64
		if resID != nil {
			rid = sql.NullInt64{Int64: resID(), Valid: true}
		}
		var afterJ sql.NullString
		if after != nil {
			afterJ = sql.NullString{String: jsonStr(after), Valid: true}
		}
		return q.CreateAudit(ctx, db.CreateAuditParams{Ts: nowMs(), ActorUserID: uid, Actor: actor, Ip: clientIP(r), Action: action,
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
	s.Hub.Publish(events.Event{Kind: "lb.updated", Ephemeral: true})
}

func (s *Server) lb(id int64) (*state.LB, error) {
	lb := s.Snap.Get().LB(id)
	if lb == nil {
		return nil, errNotFound("load balancer")
	}
	return lb, nil
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
