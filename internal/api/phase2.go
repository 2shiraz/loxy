package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/2shiraz/loxy/internal/api/gen"
	"github.com/2shiraz/loxy/internal/auth"
	"github.com/2shiraz/loxy/internal/events"
	"github.com/2shiraz/loxy/internal/model"
	"github.com/2shiraz/loxy/internal/notify"
	"github.com/2shiraz/loxy/internal/store/db"
)

// ---------- API tokens (§14.2): loxy_<prefix>_<secret>, stored as SHA-256 ----------

func hashSecret(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// bearer authenticates an API token; returns nil if the header is absent or invalid.
func (s *Server) bearer(r *http.Request) (*principal, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return nil, false
	}
	parts := strings.SplitN(strings.TrimPrefix(h, "Bearer "), "_", 3)
	if len(parts) != 3 || parts[0] != "loxy" {
		return nil, true
	}
	t, err := s.Store.Q.GetAPITokenByPrefix(r.Context(), parts[1])
	if err != nil || subtle.ConstantTimeCompare([]byte(t.SecretHash), []byte(hashSecret(parts[2]))) != 1 {
		return nil, true
	}
	now := time.Now().UnixMilli()
	if t.ExpiresAt.Valid && now > t.ExpiresAt.Int64 {
		return nil, true
	}
	if !t.LastUsedAt.Valid || now-t.LastUsedAt.Int64 > 60_000 {
		_ = s.Store.Q.TouchAPIToken(r.Context(), db.TouchAPITokenParams{LastUsedAt: sql.NullInt64{Int64: now, Valid: true}, ID: t.ID})
	}
	return &principal{User: db.User{ID: 0, Email: "token:" + t.Name, Role: t.Role}, TokenID: t.ID}, true
}

func viewToken(t db.ApiToken) map[string]any {
	return map[string]any{"id": t.ID, "name": t.Name, "prefix": t.Prefix, "role": t.Role, "expires_at": nullI64(t.ExpiresAt),
		"last_used_at": nullI64(t.LastUsedAt), "created_at": t.CreatedAt}
}

func (s *Server) ListAPITokens(w http.ResponseWriter, r *http.Request) {
	ts, err := s.Store.Q.ListAPITokens(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	out := []map[string]any{}
	for _, t := range ts {
		out = append(out, viewToken(t))
	}
	writeJSON(w, 200, map[string]any{"api_tokens": out})
}

func (s *Server) CreateAPIToken(w http.ResponseWriter, r *http.Request) {
	var in gen.APITokenCreate
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	fe := model.FieldErrors{}
	if !model.ValidName(in.Name) {
		fe.Add("name", "1–64 printable characters")
	}
	role := string(in.Role)
	if role != "admin" && role != "operator" && role != "viewer" {
		fe.Add("role", "must be admin, operator or viewer")
	}
	if p := userFrom(r); p != nil && !auth.Allows(p.User.Role, role) {
		fe.Add("role", "cannot exceed your own role")
	}
	var exp sql.NullInt64
	if in.ExpiresInDays != nil && *in.ExpiresInDays > 0 {
		exp = sql.NullInt64{Int64: time.Now().Add(time.Duration(*in.ExpiresInDays) * 24 * time.Hour).UnixMilli(), Valid: true}
	}
	if !fe.Empty() {
		fail(w, errInput(fe))
		return
	}
	prefix := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(auth.RandomToken(9)))[:8]
	secret := strings.NewReplacer("-", "x", "_", "y").Replace(auth.RandomToken(32))
	t, err := s.Store.Q.CreateAPIToken(r.Context(), db.CreateAPITokenParams{Name: in.Name, Prefix: prefix, SecretHash: hashSecret(secret), Role: role, ExpiresAt: exp, CreatedAt: nowMs()})
	if err != nil {
		fail(w, err)
		return
	}
	s.audit(r, "api_token.create", "api_token", t.ID, map[string]any{"name": in.Name, "role": role})
	writeJSON(w, 201, map[string]any{"api_token": viewToken(t), "token": "loxy_" + prefix + "_" + secret})
}

func (s *Server) DeleteAPIToken(w http.ResponseWriter, r *http.Request, id gen.Id) {
	if err := s.Store.Q.DeleteAPIToken(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	s.audit(r, "api_token.delete", "api_token", id, nil)
	w.WriteHeader(204)
}

// ---------- notifications ----------

func viewChannel(c db.NotificationChannel) map[string]any {
	return map[string]any{"id": c.ID, "name": c.Name, "kind": c.Kind, "enabled": c.Enabled != 0, "created_at": c.CreatedAt}
}

func (s *Server) ListChannels(w http.ResponseWriter, r *http.Request) {
	cs, err := s.Store.Q.ListChannels(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	out := []map[string]any{}
	for _, c := range cs {
		out = append(out, viewChannel(c))
	}
	writeJSON(w, 200, map[string]any{"notification_channels": out, "kinds": notify.Kinds})
}

func (s *Server) CreateChannel(w http.ResponseWriter, r *http.Request) {
	var in gen.NotificationChannelInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if !model.ValidName(in.Name) {
		fail(w, errField("name", "1–64 printable characters"))
		return
	}
	if err := notify.Validate(string(in.Kind), in.Config); err != nil {
		fail(w, errField("config", err.Error()))
		return
	}
	cfg, _ := json.Marshal(in.Config)
	c, err := s.Store.Q.CreateChannel(r.Context(), db.CreateChannelParams{Name: in.Name, Kind: string(in.Kind), ConfigEnc: s.Box.Seal(cfg), Enabled: 1, CreatedAt: nowMs()})
	if err != nil {
		fail(w, err)
		return
	}
	s.audit(r, "notification_channel.create", "notification_channel", c.ID, map[string]any{"name": in.Name, "kind": in.Kind})
	writeJSON(w, 201, map[string]any{"notification_channel": viewChannel(c)})
}

func (s *Server) DeleteChannel(w http.ResponseWriter, r *http.Request, id gen.Id) {
	if err := s.Store.Q.DeleteChannel(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	s.audit(r, "notification_channel.delete", "notification_channel", id, nil)
	w.WriteHeader(204)
}

func (s *Server) TestChannel(w http.ResponseWriter, r *http.Request, id gen.Id) {
	if s.Notifier == nil {
		writeErr(w, 502, "internal", "notifications disabled", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := s.Notifier.SendTo(ctx, id, events.Event{TS: nowMs(), Kind: "notification.test", Severity: "info", Message: "Test message from Loxy"}); err != nil {
		writeErr(w, 502, "internal", "delivery failed: "+err.Error(), nil)
		return
	}
	w.WriteHeader(204)
}

func viewRule(rl db.AlertRule) map[string]any {
	kinds := []string{}
	_ = json.Unmarshal([]byte(rl.EventKinds), &kinds)
	return map[string]any{"id": rl.ID, "event_kinds": kinds, "label_filter": rl.LabelFilter, "channel_id": rl.ChannelID, "min_severity": rl.MinSeverity, "cooldown_s": rl.CooldownS}
}

func (s *Server) ListAlertRules(w http.ResponseWriter, r *http.Request) {
	rs, err := s.Store.Q.ListAlertRules(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	out := []map[string]any{}
	for _, rl := range rs {
		out = append(out, viewRule(rl))
	}
	writeJSON(w, 200, map[string]any{"alert_rules": out})
}

func (s *Server) CreateAlertRule(w http.ResponseWriter, r *http.Request) {
	var in gen.AlertRuleInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	fe := model.FieldErrors{}
	kinds := []string{}
	if in.EventKinds != nil {
		kinds = *in.EventKinds
	}
	for _, k := range kinds {
		if k == "" || len(k) > 64 || strings.ContainsAny(k, " \n") {
			fe.Add("event_kinds", "invalid kind "+k)
		}
	}
	filter := ""
	if in.LabelFilter != nil {
		filter = strings.TrimSpace(*in.LabelFilter)
		if filter != "" {
			if _, err := model.ParseSelector(filter); err != nil {
				fe.Add("label_filter", err.Error())
			}
		}
	}
	sev := "warning"
	if in.MinSeverity != nil {
		sev = string(*in.MinSeverity)
	}
	if sev != "info" && sev != "warning" && sev != "error" {
		fe.Add("min_severity", "must be info, warning or error")
	}
	cool := int64(60)
	if in.CooldownS != nil {
		cool = int64(*in.CooldownS)
	}
	if cool < 0 || cool > 86400 {
		fe.Add("cooldown_s", "must be 0–86400")
	}
	if _, err := s.Store.Q.GetChannel(r.Context(), in.ChannelId); err != nil {
		fe.Add("channel_id", "unknown channel")
	}
	if !fe.Empty() {
		fail(w, errInput(fe))
		return
	}
	rl, err := s.Store.Q.CreateAlertRule(r.Context(), db.CreateAlertRuleParams{EventKinds: jsonStr(kinds), LabelFilter: filter, ChannelID: in.ChannelId, MinSeverity: sev, CooldownS: cool})
	if err != nil {
		fail(w, err)
		return
	}
	s.audit(r, "alert_rule.create", "alert_rule", rl.ID, in)
	writeJSON(w, 201, map[string]any{"alert_rule": viewRule(rl)})
}

func (s *Server) DeleteAlertRule(w http.ResponseWriter, r *http.Request, id gen.Id) {
	if err := s.Store.Q.DeleteAlertRule(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	s.audit(r, "alert_rule.delete", "alert_rule", id, nil)
	w.WriteHeader(204)
}
