// Package config reads loxy settings from LOXY_* environment variables (spec §16.4).
package config

import (
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	SecretKey       []byte
	Listen          string
	PanelPort       int
	PanelDomain     string
	PanelAllow      []string
	ACMEEmail       string
	ACMEDirectory   string
	ACMEInsecure    bool
	DataDir         string
	HAProxyConfDir  string
	HAProxyRunDir   string
	HAProxyStateDir string
	HAProxyBin      string
	StatsInterval   time.Duration
	LogLevel        string
	Dev             bool
	// LiveLogs: 0 = off, N = keep 1 of N log lines (LOXY_LIVE_LOGS=off|1|10...).
	LiveLogs int
	// NoHAProxy disables talking to the HAProxy sockets (render/validate only).
	NoHAProxy bool
	// IPv6 adds IPv6 listeners for "*" binds (LOXY_IPV6=auto|on|off; auto probes the host).
	IPv6 bool
}

// ipv6Available reports whether this network namespace (shared with HAProxy) can open
// IPv6 sockets; HAProxy refuses to start with an IPv6 listener otherwise.
func ipv6Available() bool {
	l, err := net.Listen("tcp6", "[::]:0")
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

const LetsEncryptProduction = "https://acme-v02.api.letsencrypt.org/directory"

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

func Load() (*Config, error) {
	c := &Config{
		Listen:          env("LOXY_LISTEN", "127.0.0.1:9090"),
		PanelDomain:     env("LOXY_PANEL_DOMAIN", ""),
		ACMEEmail:       env("LOXY_ACME_EMAIL", ""),
		ACMEDirectory:   env("LOXY_ACME_DIRECTORY", LetsEncryptProduction),
		DataDir:         env("LOXY_DATA_DIR", "/var/lib/loxy"),
		HAProxyConfDir:  env("LOXY_HAPROXY_CONF_DIR", "/usr/local/etc/haproxy"),
		HAProxyRunDir:   env("LOXY_HAPROXY_RUN_DIR", "/run/haproxy"),
		HAProxyStateDir: env("LOXY_HAPROXY_STATE_DIR", "/var/lib/haproxy"),
		HAProxyBin:      env("LOXY_HAPROXY_BIN", "haproxy"),
		LogLevel:        env("LOXY_LOG_LEVEL", "info"),
	}
	var err error
	if c.PanelPort, err = strconv.Atoi(env("LOXY_PANEL_PORT", "8443")); err != nil || c.PanelPort < 1 || c.PanelPort > 65535 {
		return nil, fmt.Errorf("LOXY_PANEL_PORT: invalid port")
	}
	if c.StatsInterval, err = time.ParseDuration(env("LOXY_STATS_INTERVAL", "1s")); err != nil || c.StatsInterval < 100*time.Millisecond {
		return nil, fmt.Errorf("LOXY_STATS_INTERVAL: invalid duration")
	}
	c.Dev, _ = strconv.ParseBool(env("LOXY_DEV", "false"))
	if v := env("LOXY_LIVE_LOGS", "off"); v != "off" && v != "0" {
		n, err := strconv.Atoi(strings.TrimPrefix(v, "1:"))
		if err != nil || n < 1 {
			return nil, fmt.Errorf("LOXY_LIVE_LOGS: off or a sampling rate N (keep 1 of N)")
		}
		c.LiveLogs = n
	}
	c.ACMEInsecure, _ = strconv.ParseBool(env("LOXY_ACME_INSECURE", "false"))
	c.NoHAProxy, _ = strconv.ParseBool(env("LOXY_NO_HAPROXY", "false"))
	switch v := env("LOXY_IPV6", "auto"); v {
	case "auto":
		c.IPv6 = ipv6Available()
	case "on", "true", "1":
		c.IPv6 = true
	case "off", "false", "0":
	default:
		return nil, fmt.Errorf("LOXY_IPV6: want auto, on or off, got %q", v)
	}
	for _, s := range strings.Split(env("LOXY_PANEL_ALLOW", ""), ",") {
		if s = strings.TrimSpace(s); s != "" {
			c.PanelAllow = append(c.PanelAllow, s)
		}
	}
	if k := env("LOXY_SECRET_KEY", ""); k != "" {
		b, err := base64.StdEncoding.DecodeString(k)
		if err != nil || len(b) != 32 {
			return nil, fmt.Errorf("LOXY_SECRET_KEY must be 32 bytes, base64 encoded")
		}
		c.SecretKey = b
	}
	return c, nil
}
