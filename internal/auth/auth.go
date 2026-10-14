// Package auth implements passwords (argon2id), sessions, CSRF tokens, roles and login rate limiting (spec §14).
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
	MinPassword  = 12

	IdleTimeout     = 12 * time.Hour
	AbsoluteTimeout = 7 * 24 * time.Hour
	CookieName      = "__Host-loxy_session"
	DevCookieName   = "loxy_session"
)

var b64 = base64.RawStdEncoding

func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	k := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(k)), nil
}

func CheckPassword(hash, pw string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// RandomToken returns n random bytes, URL-safe base64.
func RandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// RandomPassword is used for the initial owner password.
func RandomPassword() string { return RandomToken(18) }

func HashSessionID(id string) string {
	s := sha256.Sum256([]byte(id))
	return hex.EncodeToString(s[:])
}

// CSRFToken derives the CSRF token for a session (HMAC, not stored).
func CSRFToken(key []byte, sessionID string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("csrf:" + sessionID))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func CheckCSRF(key []byte, sessionID, token string) bool {
	return hmac.Equal([]byte(CSRFToken(key, sessionID)), []byte(token))
}

// Roles in increasing power.
var roleRank = map[string]int{"viewer": 1, "operator": 2, "admin": 3, "owner": 4}

func Allows(role, need string) bool { return roleRank[role] >= roleRank[need] && roleRank[need] > 0 }

func ValidRole(r string) bool { _, ok := roleRank[r]; return ok }

// Limiter: 5 attempts / 15 min per key, with exponential backoff afterwards.
type Limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
	now  func() time.Time
}

func NewLimiter() *Limiter { return &Limiter{hits: map[string][]time.Time{}, now: time.Now} }

var ErrRateLimited = errors.New("too many attempts")

func (l *Limiter) Allow(keys ...string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for _, k := range keys {
		var kept []time.Time
		for _, t := range l.hits[k] {
			if now.Sub(t) < 15*time.Minute {
				kept = append(kept, t)
			}
		}
		l.hits[k] = kept
		if n := len(kept); n >= 5 {
			back := time.Duration(1<<min(n-5, 10)) * time.Second
			if now.Sub(kept[n-1]) < back || n >= 20 {
				return ErrRateLimited
			}
		}
	}
	return nil
}

func (l *Limiter) Fail(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		l.hits[k] = append(l.hits[k], l.now())
	}
}

func (l *Limiter) Reset(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		delete(l.hits, k)
	}
}
