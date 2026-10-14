// Package model holds domain validation rules shared by the API and the renderer (spec §14.4).
package model

import (
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

var (
	labelKeyRe   = regexp.MustCompile(`^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*/)?[a-zA-Z0-9]([-a-zA-Z0-9_.]{0,61}[a-zA-Z0-9])?$`)
	labelValueRe = regexp.MustCompile(`^([a-zA-Z0-9]([-a-zA-Z0-9_.]{0,61}[a-zA-Z0-9])?)?$`)
	hcPathRe     = regexp.MustCompile(`^/[A-Za-z0-9._~!&()*+,;=:@%/?-]{0,254}$`)
	cookieRe     = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	statusItemRe = regexp.MustCompile(`^[1-5][0-9?]{2}$`)
	hostLabelRe  = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
)

// FieldErrors collects per-field validation messages.
type FieldErrors map[string][]string

func (f FieldErrors) Add(field, msg string) { f[field] = append(f[field], msg) }
func (f FieldErrors) Empty() bool           { return len(f) == 0 }
func (f FieldErrors) Error() string {
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(k + ": " + strings.Join(f[k], ", "))
	}
	return b.String()
}

func ValidName(s string) bool {
	if len(s) == 0 || len([]rune(s)) > 64 {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

func ValidateLabels(fe FieldErrors, field string, labels map[string]string) {
	for k, v := range labels {
		if !labelKeyRe.MatchString(k) {
			fe.Add(field, fmt.Sprintf("invalid label key %q", k))
		}
		if !labelValueRe.MatchString(v) {
			fe.Add(field, fmt.Sprintf("invalid label value %q", v))
		}
	}
}

func ValidLabelKey(k string) bool   { return labelKeyRe.MatchString(k) }
func ValidLabelValue(v string) bool { return labelValueRe.MatchString(v) }

// ValidHostname checks an RFC 1123 hostname (lowercase, ≤253). allowWildcard permits a leading "*.".
func ValidHostname(s string, allowWildcard bool) bool {
	if allowWildcard && strings.HasPrefix(s, "*.") {
		s = s[2:]
	}
	if len(s) == 0 || len(s) > 253 || s != strings.ToLower(s) {
		return false
	}
	parts := strings.Split(strings.TrimSuffix(s, "."), ".")
	if len(parts) < 1 {
		return false
	}
	for _, p := range parts {
		if !hostLabelRe.MatchString(p) {
			return false
		}
	}
	return true
}

func ValidHCPath(s string) bool     { return hcPathRe.MatchString(s) }
func ValidCookieName(s string) bool { return cookieRe.MatchString(s) }
func ValidPort(p int) bool          { return p >= 1 && p <= 65535 }

// ValidHCResponse: printable ASCII except '"\#$, 1–128 chars.
func ValidHCResponse(s string) bool {
	if len(s) < 1 || len(s) > 128 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c > 0x7e || strings.IndexByte(`'"\#$`, c) >= 0 {
			return false
		}
	}
	return true
}

// ParseTargetIP parses an IP and rejects addresses that can never be a target.
func ParseTargetIP(s string) (netip.Addr, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return a, fmt.Errorf("not an IP address")
	}
	if a.Zone() != "" {
		return a, fmt.Errorf("zones are not allowed")
	}
	switch {
	case a.IsLoopback():
		return a, fmt.Errorf("loopback addresses are not allowed")
	case a.IsLinkLocalUnicast(), a.IsLinkLocalMulticast():
		return a, fmt.Errorf("link-local addresses are not allowed")
	case a.IsMulticast():
		return a, fmt.Errorf("multicast addresses are not allowed")
	case a.IsUnspecified():
		return a, fmt.Errorf("unspecified address is not allowed")
	}
	return a.Unmap(), nil
}

// StatusCodesToHAProxy converts items like "2??", "20?", "204" into HAProxy's
// `http-check expect status` list: ranges merged, de-duplicated, sorted.
func StatusCodesToHAProxy(items []string) (string, error) {
	if len(items) == 0 || len(items) > 20 {
		return "", fmt.Errorf("1 to 20 status codes required")
	}
	type rng struct{ lo, hi int }
	var rs []rng
	for _, it := range items {
		it = strings.TrimSpace(it)
		if !statusItemRe.MatchString(it) {
			return "", fmt.Errorf("invalid status code %q", it)
		}
		q := strings.Index(it, "?")
		if q >= 0 && strings.ContainsAny(it[q:], "0123456789") {
			return "", fmt.Errorf("invalid status code %q: wildcards must be trailing", it)
		}
		lo, _ := strconv.Atoi(strings.ReplaceAll(it, "?", "0"))
		hi, _ := strconv.Atoi(strings.ReplaceAll(it, "?", "9"))
		rs = append(rs, rng{lo, hi})
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].lo < rs[j].lo || (rs[i].lo == rs[j].lo && rs[i].hi < rs[j].hi) })
	merged := []rng{rs[0]}
	for _, r := range rs[1:] {
		last := &merged[len(merged)-1]
		if r.lo <= last.hi+1 {
			if r.hi > last.hi {
				last.hi = r.hi
			}
			continue
		}
		merged = append(merged, r)
	}
	out := make([]string, len(merged))
	for i, r := range merged {
		if r.lo == r.hi {
			out[i] = strconv.Itoa(r.lo)
		} else {
			out[i] = fmt.Sprintf("%d-%d", r.lo, r.hi)
		}
	}
	return strings.Join(out, ","), nil
}
