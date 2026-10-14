// Package events is the in-process pub/sub used for SSE, notifications and the reconciler (spec §12.5).
package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/2shiraz/loxy/internal/store/db"
)

type Event struct {
	ID       int64          `json:"id"`
	TS       int64          `json:"ts"`
	LBID     *int64         `json:"lb_id"`
	Kind     string         `json:"kind"`
	Severity string         `json:"severity"`
	Message  string         `json:"message"`
	Data     map[string]any `json:"data,omitempty"`
	// Ephemeral events are only streamed, not stored (e.g. lb.updated).
	Ephemeral bool `json:"-"`
}

type Hub struct {
	mu     sync.Mutex
	subs   map[chan Event]struct{}
	replay []Event
	seq    int64
	q      *db.Queries
}

func NewHub(q *db.Queries) *Hub { return &Hub{subs: map[chan Event]struct{}{}, q: q} }

// Publish stores (unless ephemeral) and broadcasts an event.
func (h *Hub) Publish(e Event) {
	if e.TS == 0 {
		e.TS = time.Now().UnixMilli()
	}
	if e.Severity == "" {
		e.Severity = "info"
	}
	if !e.Ephemeral && h.q != nil {
		data, _ := json.Marshal(e.Data)
		if e.Data == nil {
			data = []byte("{}")
		}
		var lb sql.NullInt64
		if e.LBID != nil {
			lb = sql.NullInt64{Int64: *e.LBID, Valid: true}
		}
		id, err := h.q.CreateEvent(context.Background(), db.CreateEventParams{Ts: e.TS, LbID: lb, Kind: e.Kind, Severity: e.Severity, Message: e.Message, Data: string(data)})
		if err != nil {
			slog.Warn("store event", "err", err)
		}
		e.ID = id
	}
	h.mu.Lock()
	h.seq++
	if e.ID == 0 {
		e.ID = -h.seq
	}
	h.replay = append(h.replay, e)
	cut := time.Now().Add(-2 * time.Minute).UnixMilli()
	i := 0
	for i < len(h.replay) && h.replay[i].TS < cut {
		i++
	}
	h.replay = h.replay[i:]
	for ch := range h.subs {
		select {
		case ch <- e:
		default: // slow subscriber: drop
		}
	}
	h.mu.Unlock()
}

// Subscribe returns a channel of events and a cancel func. Events after sinceTS are replayed.
func (h *Hub) Subscribe(sinceTS int64) (<-chan Event, func()) {
	ch := make(chan Event, 256)
	h.mu.Lock()
	if sinceTS > 0 {
		for _, e := range h.replay {
			if e.TS > sinceTS {
				ch <- e
			}
		}
	}
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

func I64(v int64) *int64 { return &v }
