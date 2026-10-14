package model

import (
	"fmt"
	"strings"
)

// Selector is a parsed label selector: `key=value`, `key!=value`, `key in (a,b)`,
// `key notin (a,b)`, `key`, `!key`; comma = AND. It is never passed to HAProxy.
type Selector []Requirement

type Op string

const (
	OpEq     Op = "="
	OpNeq    Op = "!="
	OpIn     Op = "in"
	OpNotIn  Op = "notin"
	OpExists Op = "exists"
	OpAbsent Op = "!"
)

type Requirement struct {
	Key    string
	Op     Op
	Values []string
}

func ParseSelector(s string) (Selector, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty selector")
	}
	var parts []string
	depth, start := 0, 0
	for i, c := range s {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("unbalanced parenthesis")
			}
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("unbalanced parenthesis")
	}
	parts = append(parts, s[start:])
	var sel Selector
	for _, p := range parts {
		r, err := parseReq(strings.TrimSpace(p))
		if err != nil {
			return nil, err
		}
		sel = append(sel, r)
	}
	return sel, nil
}

func parseReq(p string) (Requirement, error) {
	if p == "" {
		return Requirement{}, fmt.Errorf("empty requirement")
	}
	check := func(r Requirement) (Requirement, error) {
		if !ValidLabelKey(r.Key) {
			return r, fmt.Errorf("invalid label key %q", r.Key)
		}
		for _, v := range r.Values {
			if !ValidLabelValue(v) {
				return r, fmt.Errorf("invalid label value %q", v)
			}
		}
		return r, nil
	}
	if i := strings.Index(p, "!="); i > 0 {
		return check(Requirement{Key: strings.TrimSpace(p[:i]), Op: OpNeq, Values: []string{strings.TrimSpace(p[i+2:])}})
	}
	if i := strings.Index(p, "="); i > 0 {
		v := strings.TrimSpace(strings.TrimPrefix(p[i+1:], "="))
		return check(Requirement{Key: strings.TrimSpace(p[:i]), Op: OpEq, Values: []string{v}})
	}
	if strings.HasPrefix(p, "!") {
		return check(Requirement{Key: strings.TrimSpace(p[1:]), Op: OpAbsent})
	}
	fields := strings.Fields(p)
	if len(fields) == 1 {
		return check(Requirement{Key: fields[0], Op: OpExists})
	}
	if len(fields) >= 2 && (fields[1] == "in" || fields[1] == "notin" || strings.HasPrefix(fields[1], "in(") || strings.HasPrefix(fields[1], "notin(")) {
		key := fields[0]
		rest := strings.TrimSpace(p[len(key):])
		op := OpIn
		if strings.HasPrefix(rest, "notin") {
			op, rest = OpNotIn, strings.TrimSpace(rest[5:])
		} else {
			rest = strings.TrimSpace(rest[2:])
		}
		if !strings.HasPrefix(rest, "(") || !strings.HasSuffix(rest, ")") {
			return Requirement{}, fmt.Errorf("expected (values) after %s", op)
		}
		var vals []string
		for _, v := range strings.Split(rest[1:len(rest)-1], ",") {
			vals = append(vals, strings.TrimSpace(v))
		}
		return check(Requirement{Key: key, Op: op, Values: vals})
	}
	return Requirement{}, fmt.Errorf("cannot parse %q", p)
}

func (s Selector) Matches(labels map[string]string) bool {
	for _, r := range s {
		v, ok := labels[r.Key]
		switch r.Op {
		case OpEq:
			if !ok || v != r.Values[0] {
				return false
			}
		case OpNeq:
			if ok && v == r.Values[0] {
				return false
			}
		case OpExists:
			if !ok {
				return false
			}
		case OpAbsent:
			if ok {
				return false
			}
		case OpIn:
			if !ok || !contains(r.Values, v) {
				return false
			}
		case OpNotIn:
			if ok && contains(r.Values, v) {
				return false
			}
		}
	}
	return true
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
