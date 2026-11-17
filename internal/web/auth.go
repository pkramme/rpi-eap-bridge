package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"hash"
	"strconv"
	"strings"
	"sync"
	"time"
)

const pbkdf2Iterations = 120_000

// HashPassword hashes an admin password with PBKDF2-HMAC-SHA256.
// Format: pbkdf2-sha256$<iterations>$<salt>$<hash> (base64, no padding).
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dk := pbkdf2([]byte(password), salt, pbkdf2Iterations, 32, sha256.New)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdf2Iterations, enc.EncodeToString(salt), enc.EncodeToString(dk)), nil
}

// VerifyPassword checks a password against a hash from HashPassword.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 || iter > 10_000_000 {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(parts[2])
	want, err2 := enc.DecodeString(parts[3])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got := pbkdf2([]byte(password), salt, iter, len(want), sha256.New)
	return subtle.ConstantTimeCompare(got, want) == 1
}

// pbkdf2 implements RFC 8018 PBKDF2 (crypto/pbkdf2 needs Go 1.24).
func pbkdf2(password, salt []byte, iter, keyLen int, h func() hash.Hash) []byte {
	prf := hmac.New(h, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen
	var buf [4]byte
	dk := make([]byte, 0, numBlocks*hashLen)
	u := make([]byte, hashLen)
	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		buf[0], buf[1], buf[2], buf[3] = byte(block>>24), byte(block>>16), byte(block>>8), byte(block)
		prf.Write(buf[:4])
		dk = prf.Sum(dk)
		t := dk[len(dk)-hashLen:]
		copy(u, t)
		for n := 2; n <= iter; n++ {
			prf.Reset()
			prf.Write(u)
			u = u[:0]
			u = prf.Sum(u)
			for x := range u {
				t[x] ^= u[x]
			}
		}
	}
	return dk[:keyLen]
}

// Session tokens are stateless: "<expiry>.<hmac>". The HMAC key is derived
// from the session secret and the password hash, so changing the password
// logs out every session.
func sessionKey(secret, passwordHash string) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte("session:" + passwordHash))
	return m.Sum(nil)
}

func makeSession(key []byte, expires time.Time) string {
	e := strconv.FormatInt(expires.Unix(), 10)
	m := hmac.New(sha256.New, key)
	m.Write([]byte(e))
	return e + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func checkSession(key []byte, token string, now time.Time) bool {
	e, sig, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(e, 10, 64)
	if err != nil || now.Unix() > exp {
		return false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return false
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(e))
	return hmac.Equal(got, m.Sum(nil))
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// limiter slows down password guessing: after 5 wrong attempts each further
// attempt has to wait (2, 4, 8 … up to 64 seconds).
type limiter struct {
	mu    sync.Mutex
	fails int
	last  time.Time
}

func (l *limiter) wait(now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fails < 5 {
		return 0
	}
	shift := l.fails - 4
	if shift > 6 {
		shift = 6
	}
	d := time.Duration(1<<shift)*time.Second - now.Sub(l.last)
	if d < 0 {
		return 0
	}
	return d
}

func (l *limiter) fail(now time.Time) {
	l.mu.Lock()
	l.fails++
	l.last = now
	l.mu.Unlock()
}

func (l *limiter) reset() {
	l.mu.Lock()
	l.fails = 0
	l.mu.Unlock()
}
