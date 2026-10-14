// Package notify delivers events to notification channels according to alert rules
// (spec §8.15, Phase 2): webhook, Slack, Telegram and email (SMTP).
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/2shiraz/loxy/internal/events"
	"github.com/2shiraz/loxy/internal/model"
	"github.com/2shiraz/loxy/internal/secrets"
	"github.com/2shiraz/loxy/internal/store/db"
)

// Field describes a channel configuration field.
type Field struct {
	Name   string `json:"name"`
	Label  string `json:"label"`
	Secret bool   `json:"secret"`
}

// Kinds lists the supported channel kinds and their configuration fields.
var Kinds = map[string][]Field{
	"webhook":  {{"url", "URL (receives the event as JSON)", false}, {"secret", "Shared secret (sent as X-Loxy-Secret)", true}},
	"slack":    {{"webhook_url", "Incoming webhook URL", true}},
	"telegram": {{"bot_token", "Bot token", true}, {"chat_id", "Chat ID", false}},
	"email":    {{"smtp_host", "SMTP host:port", false}, {"username", "Username", false}, {"password", "Password", true}, {"from", "From", false}, {"to", "To (comma separated)", false}},
}

// Validate checks a channel config.
func Validate(kind string, cfg map[string]string) error {
	fields, ok := Kinds[kind]
	if !ok {
		return fmt.Errorf("unknown channel kind %q", kind)
	}
	for _, f := range fields {
		if f.Name == "secret" || f.Name == "username" || f.Name == "password" {
			continue
		}
		if strings.TrimSpace(cfg[f.Name]) == "" {
			return fmt.Errorf("%s is required", f.Label)
		}
	}
	for _, k := range []string{"url", "webhook_url"} {
		if v := cfg[k]; v != "" {
			u, err := url.Parse(v)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				return fmt.Errorf("%s must be an http(s) URL", k)
			}
		}
	}
	if h := cfg["smtp_host"]; h != "" {
		if _, _, err := net.SplitHostPort(h); err != nil {
			return fmt.Errorf("smtp_host must be host:port")
		}
	}
	return nil
}

var sevRank = map[string]int{"info": 1, "warning": 2, "error": 3}

// Notifier subscribes to the event hub and dispatches matching events.
type Notifier struct {
	Q        *db.Queries
	Box      *secrets.Box
	Hub      *events.Hub
	LBLabels func(lbID int64) map[string]string
	HTTP     *http.Client

	mu   sync.Mutex
	last map[string]time.Time // rule|lb|kind → last sent
}

func New(q *db.Queries, box *secrets.Box, hub *events.Hub, lbLabels func(int64) map[string]string) *Notifier {
	return &Notifier{Q: q, Box: box, Hub: hub, LBLabels: lbLabels, HTTP: &http.Client{Timeout: 10 * time.Second}, last: map[string]time.Time{}}
}

func (n *Notifier) Run(ctx context.Context) {
	ch, cancel := n.Hub.Subscribe(0)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-ch:
			if e.Ephemeral {
				continue
			}
			n.handle(ctx, e)
		}
	}
}

// Matches reports whether a rule applies to an event (pure, tested).
func Matches(r db.AlertRule, e events.Event, lbLabels map[string]string) bool {
	if sevRank[e.Severity] < sevRank[r.MinSeverity] {
		return false
	}
	var kinds []string
	_ = json.Unmarshal([]byte(r.EventKinds), &kinds)
	if len(kinds) > 0 {
		ok := false
		for _, k := range kinds {
			if k == e.Kind || (strings.HasSuffix(k, "*") && strings.HasPrefix(e.Kind, strings.TrimSuffix(k, "*"))) {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	if r.LbFilter != "" {
		sel, err := model.ParseSelector(r.LbFilter)
		if err != nil || lbLabels == nil || !sel.Matches(lbLabels) {
			return false
		}
	}
	return true
}

func (n *Notifier) handle(ctx context.Context, e events.Event) {
	rules, err := n.Q.ListAlertRules(ctx)
	if err != nil || len(rules) == 0 {
		return
	}
	var labels map[string]string
	var lb int64
	if e.LBID != nil {
		lb = *e.LBID
		if n.LBLabels != nil {
			labels = n.LBLabels(lb)
		}
	}
	for _, r := range rules {
		if !Matches(r, e, labels) {
			continue
		}
		key := fmt.Sprintf("%d|%d|%s", r.ID, lb, e.Kind)
		n.mu.Lock()
		if t, ok := n.last[key]; ok && time.Since(t) < time.Duration(r.CooldownS)*time.Second {
			n.mu.Unlock()
			continue
		}
		n.last[key] = time.Now()
		n.mu.Unlock()
		go func(chID int64) {
			c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := n.SendTo(c, chID, e); err != nil {
				slog.Warn("notification failed", "channel", chID, "err", err)
			}
		}(r.ChannelID)
	}
}

// SendTo delivers one event to a channel.
func (n *Notifier) SendTo(ctx context.Context, channelID int64, e events.Event) error {
	ch, err := n.Q.GetChannel(ctx, channelID)
	if err != nil {
		return err
	}
	if ch.Enabled == 0 {
		return nil
	}
	raw, err := n.Box.Open(ch.ConfigEnc)
	if err != nil {
		return err
	}
	cfg := map[string]string{}
	_ = json.Unmarshal(raw, &cfg)
	text := fmt.Sprintf("[Loxy] %s %s: %s", strings.ToUpper(e.Severity), e.Kind, e.Message)
	switch ch.Kind {
	case "webhook":
		b, _ := json.Marshal(e)
		return n.post(ctx, cfg["url"], b, map[string]string{"X-Loxy-Secret": cfg["secret"]})
	case "slack":
		b, _ := json.Marshal(map[string]string{"text": text})
		return n.post(ctx, cfg["webhook_url"], b, nil)
	case "telegram":
		b, _ := json.Marshal(map[string]string{"chat_id": cfg["chat_id"], "text": text})
		return n.post(ctx, "https://api.telegram.org/bot"+cfg["bot_token"]+"/sendMessage", b, nil)
	case "email":
		host, _, _ := net.SplitHostPort(cfg["smtp_host"])
		var auth smtp.Auth
		if cfg["username"] != "" {
			auth = smtp.PlainAuth("", cfg["username"], cfg["password"], host)
		}
		to := strings.Split(cfg["to"], ",")
		for i := range to {
			to[i] = strings.TrimSpace(to[i])
		}
		msg := "From: " + cfg["from"] + "\r\nTo: " + strings.Join(to, ", ") + "\r\nSubject: " + text + "\r\n\r\n" + e.Message + "\r\n"
		return smtp.SendMail(cfg["smtp_host"], auth, cfg["from"], to, []byte(msg))
	}
	return fmt.Errorf("unknown kind %s", ch.Kind)
}

func (n *Notifier) post(ctx context.Context, u string, body []byte, hdr map[string]string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := n.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
