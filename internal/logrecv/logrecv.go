// Package logrecv receives HAProxy's UDP syslog (127.0.0.1:5514) and keeps a sampled ring
// buffer of recent lines per load balancer for the live Logs tab (spec §20). Off by default.
package logrecv

import (
	"context"
	"log/slog"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const perLB = 500

var proxyRe = regexp.MustCompile(`\blb(\d+)_s\d+_(?:fe|rd)\b|\bsys_acme_lb(\d+)\b`)

type Line struct {
	TS   int64  `json:"ts"`
	Text string `json:"text"`
}

type Receiver struct {
	Addr   string
	Sample int // keep 1 of N lines (1 = all)

	mu    sync.Mutex
	rings map[int64][]Line
	subs  map[int64]map[chan Line]struct{}
	seen  atomic.Uint64
}

func New(addr string, sample int) *Receiver {
	if sample < 1 {
		sample = 1
	}
	return &Receiver{Addr: addr, Sample: sample, rings: map[int64][]Line{}, subs: map[int64]map[chan Line]struct{}{}}
}

// ParseLB extracts the LB id from a log line, 0 when not an LB line.
func ParseLB(line string) int64 {
	m := proxyRe.FindStringSubmatch(line)
	if m == nil {
		return 0
	}
	s := m[1]
	if s == "" {
		s = m[2]
	}
	id, _ := strconv.ParseInt(s, 10, 64)
	return id
}

// stripSyslog drops the RFC 5424 header ("<pri>1 ts host app pid msgid sd ") and keeps the message.
func stripSyslog(s string) string {
	s = strings.TrimRight(s, "\n\x00")
	if strings.HasPrefix(s, "<") {
		f := strings.SplitN(s, " ", 8)
		if len(f) == 8 {
			return f[7]
		}
	}
	return s
}

func (r *Receiver) Run(ctx context.Context) {
	pc, err := net.ListenPacket("udp", r.Addr)
	if err != nil {
		slog.Warn("live logs: cannot listen", "addr", r.Addr, "err", err)
		return
	}
	go func() { <-ctx.Done(); _ = pc.Close() }()
	buf := make([]byte, 9000)
	for {
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		if r.seen.Add(1)%uint64(r.Sample) != 0 {
			continue
		}
		r.Add(string(buf[:n]))
	}
}

// Add records one raw syslog datagram.
func (r *Receiver) Add(raw string) {
	msg := stripSyslog(raw)
	lb := ParseLB(msg)
	if lb == 0 {
		return
	}
	l := Line{TS: time.Now().UnixMilli(), Text: msg}
	r.mu.Lock()
	ring := append(r.rings[lb], l)
	if len(ring) > perLB {
		ring = ring[len(ring)-perLB:]
	}
	r.rings[lb] = ring
	for ch := range r.subs[lb] {
		select {
		case ch <- l:
		default:
		}
	}
	r.mu.Unlock()
}

// Subscribe returns recent lines and a live channel for one LB.
func (r *Receiver) Subscribe(lb int64) ([]Line, <-chan Line, func()) {
	ch := make(chan Line, 64)
	r.mu.Lock()
	recent := append([]Line(nil), r.rings[lb]...)
	if r.subs[lb] == nil {
		r.subs[lb] = map[chan Line]struct{}{}
	}
	r.subs[lb][ch] = struct{}{}
	r.mu.Unlock()
	return recent, ch, func() {
		r.mu.Lock()
		delete(r.subs[lb], ch)
		r.mu.Unlock()
	}
}
