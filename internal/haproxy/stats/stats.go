// Package stats parses `show stat` (CSV, columns by header name) and `show info` (spec §10.1).
package stats

import (
	"strconv"
	"strings"
)

// Header maps column names to indexes; shared by all rows of one response.
type Header map[string]int

// Row is one proxy/server line from `show stat`.
type Row struct {
	PxName, SvName string
	Type           int // 0 frontend, 1 backend, 2 server, 3 listener
	line           string
	ends           []int32 // ends[i] = index of the comma after field i (zero-copy field access)
	hdr            Header
}

func (r *Row) field(i int) string {
	if i >= len(r.ends) {
		return ""
	}
	start := 0
	if i > 0 {
		start = int(r.ends[i-1]) + 1
	}
	return r.line[start:r.ends[i]]
}

func (r *Row) Str(k string) string {
	if i, ok := r.hdr[k]; ok {
		return r.field(i)
	}
	return ""
}

func (r *Row) Int(k string) int64 {
	n, _ := strconv.ParseInt(r.Str(k), 10, 64)
	return n
}

// ParseStat parses the CSV output of `show stat`. Columns are looked up by name, never position.
func ParseStat(out string) []Row {
	var hdr Header
	// One backing array for all field offsets keeps allocations per poll constant.
	backing := make([]int32, 0, strings.Count(out, ",")+strings.Count(out, "\n")+1)
	rows := make([]Row, 0, strings.Count(out, "\n"))
	for len(out) > 0 {
		var l string
		if i := strings.IndexByte(out, '\n'); i >= 0 {
			l, out = out[:i], out[i+1:]
		} else {
			l, out = out, ""
		}
		if strings.HasPrefix(l, "# ") {
			names := strings.Split(strings.TrimPrefix(l, "# "), ",")
			hdr = make(Header, len(names))
			for i, n := range names {
				if n != "" {
					hdr[n] = i
				}
			}
			continue
		}
		if l == "" || hdr == nil {
			continue
		}
		start := len(backing)
		for i := 0; i < len(l); i++ {
			if l[i] == ',' {
				backing = append(backing, int32(i))
			}
		}
		backing = append(backing, int32(len(l)))
		r := Row{line: l, ends: backing[start:len(backing):len(backing)], hdr: hdr}
		r.PxName, r.SvName = r.Str("pxname"), r.Str("svname")
		r.Type, _ = strconv.Atoi(r.Str("type"))
		rows = append(rows, r)
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
