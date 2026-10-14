// Package stats parses `show stat` (CSV, columns by header name) and `show info` (spec §10.1).
package stats

import (
	"strconv"
	"strings"
)

// Row is one proxy/server line from `show stat`.
type Row struct {
	PxName, SvName string
	Type           int // 0 frontend, 1 backend, 2 server, 3 listener
	f              map[string]string
}

func (r *Row) Str(k string) string { return r.f[k] }
func (r *Row) Int(k string) int64 {
	n, _ := strconv.ParseInt(r.f[k], 10, 64)
	return n
}

// ParseStat parses the CSV output of `show stat`.
func ParseStat(out string) []Row {
	lines := strings.Split(out, "\n")
	var hdr []string
	var rows []Row
	for _, l := range lines {
		if strings.HasPrefix(l, "# ") {
			hdr = strings.Split(strings.TrimPrefix(l, "# "), ",")
			continue
		}
		if l == "" || hdr == nil {
			continue
		}
		cols := strings.Split(l, ",")
		m := make(map[string]string, len(hdr))
		for i, h := range hdr {
			if i < len(cols) && h != "" {
				m[h] = cols[i]
			}
		}
		t, _ := strconv.Atoi(m["type"])
		rows = append(rows, Row{PxName: m["pxname"], SvName: m["svname"], Type: t, f: m})
	}
	return rows
}

// ParseInfo parses `show info` "Key: value" lines.
func ParseInfo(out string) map[string]string {
	m := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(l, ":"); ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}
