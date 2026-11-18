package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

const sessionCookie = "guoapp_web"
const sessionDuration = 12 * time.Hour

type webAuth struct {
	password [sha256.Size]byte
	secure   bool
	mu       sync.Mutex
	sessions map[string]time.Time
	failures int
	resetAt  time.Time
}

func (auth *webAuth) authorized(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	auth.mu.Lock()
	defer auth.mu.Unlock()
	return time.Now().Before(auth.sessions[cookie.Value])
}

func (auth *webAuth) login(w http.ResponseWriter, password string) int {
	hash := sha256.Sum256([]byte(password))
	now := time.Now()
	auth.mu.Lock()
	defer auth.mu.Unlock()
	if !now.Before(auth.resetAt) {
		auth.failures = 0
		auth.resetAt = now.Add(time.Minute)
	}
	if auth.failures >= 8 {
		w.Header().Set("Retry-After", "60")
		return http.StatusTooManyRequests
	}
	if subtle.ConstantTimeCompare(hash[:], auth.password[:]) != 1 {
		auth.failures++
		return http.StatusUnauthorized
	}
	for token, expires := range auth.sessions {
		if !now.Before(expires) {
			delete(auth.sessions, token)
		}
	}
	if len(auth.sessions) >= 128 {
		return http.StatusServiceUnavailable
	}
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return http.StatusServiceUnavailable
	}
	token := hex.EncodeToString(bytes[:])
	auth.sessions[token] = now.Add(sessionDuration)
	auth.failures = 0
	auth.setCookie(w, token, int(sessionDuration.Seconds()))
	return http.StatusSeeOther
}

func (auth *webAuth) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		auth.mu.Lock()
		delete(auth.sessions, cookie.Value)
		auth.mu.Unlock()
	}
	auth.setCookie(w, "", -1)
}

func (auth *webAuth) setCookie(w http.ResponseWriter, token string, age int) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", MaxAge: age,
		HttpOnly: true, Secure: auth.secure, SameSite: http.SameSiteStrictMode,
	})
}
