// Package runtime is a client for HAProxy's worker (admin.sock) and master CLI sockets (spec §7.1).
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

type Client struct {
	AdminSock  string
	MasterSock string
	sem        chan struct{}
}

func New(admin, master string) *Client {
	return &Client{AdminSock: admin, MasterSock: master, sem: make(chan struct{}, 4)}
}

func (c *Client) do(ctx context.Context, sock, cmd string, timeout time.Duration) (string, error) {
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-c.sem }()
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "unix", sock)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := io.WriteString(conn, cmd+"\n"); err != nil {
		return "", err
	}
	// The master CLI only answers once the client half-closes (verified, docs/haproxy-notes.md).
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	b, err := io.ReadAll(conn)
	if err != nil && !errors.Is(err, io.EOF) {
		return string(b), err
	}
	return string(b), nil
}

// Exec runs one worker command (one connection per command).
func (c *Client) Exec(ctx context.Context, cmd string) (string, error) {
	if strings.ContainsAny(cmd, "\n;") {
		return "", fmt.Errorf("refusing command with newline or ';'")
	}
	return c.do(ctx, c.AdminSock, cmd, 5*time.Second)
}

// ExecPayload runs a command with a `<<` payload (PEM, crt-list lines). PEM never has blank lines.
func (c *Client) ExecPayload(ctx context.Context, cmd, payload string) (string, error) {
	if strings.ContainsAny(cmd, "\n;") {
		return "", fmt.Errorf("refusing command with newline or ';'")
	}
	var lines []string
	for _, l := range strings.Split(payload, "\n") {
		if l = strings.TrimRight(l, " \t\r"); l != "" {
			lines = append(lines, l)
		}
	}
	return c.do(ctx, c.AdminSock, cmd+" <<\n"+strings.Join(lines, "\n")+"\n", 10*time.Second)
}

// Run executes a command and checks its output against the matcher table.
func (c *Client) Run(ctx context.Context, cmd string) error {
	out, err := c.Exec(ctx, cmd)
	if err != nil {
		return err
	}
	return Check(cmd, out)
}

func (c *Client) RunPayload(ctx context.Context, cmd, payload string) error {
	out, err := c.ExecPayload(ctx, cmd, payload)
	if err != nil {
		return err
	}
	return Check(cmd, out)
}

// ReloadResult is the parsed answer of the master's synchronous `reload`.
type ReloadResult struct {
	Success bool
	Output  string
}

// Reload sends the synchronous master CLI `reload` and waits for Success=1/0.
func (c *Client) Reload(ctx context.Context) (*ReloadResult, error) {
	out, err := c.do(ctx, c.MasterSock, "reload", 30*time.Second)
	if err != nil {
		return nil, err
	}
	return ParseReload(out), nil
}

func ParseReload(out string) *ReloadResult {
	r := &ReloadResult{Output: out}
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) == "Success=1" {
			r.Success = true
		}
	}
	return r
}

// Master runs a master CLI command (e.g. `show proc`).
func (c *Client) Master(ctx context.Context, cmd string) (string, error) {
	return c.do(ctx, c.MasterSock, cmd, 5*time.Second)
}

// matcher describes the expected success output of a command family.
type matcher struct {
	prefix string
	ok     func(out string) bool
}

func empty(out string) bool { return strings.TrimSpace(out) == "" }
func has(s ...string) func(string) bool {
	return func(out string) bool {
		for _, x := range s {
			if !strings.Contains(out, x) {
				return false
			}
		}
		return true
	}
}

// Matchers is the single table of expected outputs (§7.1). Longest prefix wins.
var Matchers = []matcher{
	{"add server ", has("New server registered.")},
	{"del server ", has("Server deleted.")},
	{"enable server ", empty},
	{"disable server ", empty},
	{"enable health ", empty},
	{"disable health ", empty},
	{"set server ", empty},
	{"add map ", empty},
	{"del map ", empty},
	{"clear map ", empty},
	{"set ssl cert ", func(o string) bool {
		return strings.Contains(o, "Transaction created for certificate") || strings.Contains(o, "Transaction updated for certificate")
	}},
	{"commit ssl cert ", has("Success!")},
	{"new ssl cert ", has("New empty certificate store")},
	{"add ssl crt-list ", has("Success!")},
	{"del ssl crt-list ", has("deleted in crtlist")},
	{"del ssl cert ", has("deleted!")},
	{"wait ", func(o string) bool {
		return !strings.Contains(strings.ToLower(o), "error") && !strings.Contains(o, "timeout")
	}},
}

// Check validates output for cmd. Unknown commands must produce empty output.
func Check(cmd, out string) error {
	best := -1
	for i, m := range Matchers {
		if strings.HasPrefix(cmd, m.prefix) && (best < 0 || len(m.prefix) > len(Matchers[best].prefix)) {
			best = i
		}
	}
	ok := empty(out)
	if best >= 0 {
		ok = Matchers[best].ok(out)
	}
	if !ok {
		return fmt.Errorf("haproxy: %q: unexpected output: %s", cmd, strings.TrimSpace(out))
	}
	return nil
}
