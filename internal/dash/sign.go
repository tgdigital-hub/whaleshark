package dash

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// The page's own files: the key in the login's settings folder, the rest in
// its state folder.
const (
	keyFile   = "dash.key"
	countFile = "dash.count"
	sockFile  = "dash.sock"
	lockFile  = "dash.lock"

	codeLife    = time.Minute
	sessionLife = 30 * 24 * time.Hour
	// A session is given a new end once it has been in use for this long.
	renewAfter = 24 * time.Hour
	// From the fifth wrong sign-in code on, the next is not looked at for a
	// second, then two, doubling up to this.
	slowFrom, slowMost = 5, 6
)

var b64 = base64.RawURLEncoding

// readKey returns the login's key. The server makes one where there is
// none, and fresh has a new one written, which signs every browser out; with
// neither, a missing key is an error.
func readKey(p contract.Platform, dirs contract.Dirs, fresh, make bool) ([]byte, error) {
	path := filepath.Join(dirs.Config, keyFile)
	key, err := p.Read(path)
	switch {
	case fresh, make && errors.Is(err, fs.ErrNotExist):
		key = []byte(rand.Text() + rand.Text())
		return key, replace(p, path, key)
	case err == nil && len(key) < sha256.Size:
		err = errors.New(keyFile + " is too short to be a key")
	}
	return key, err
}

// replace puts a small private file in place whole, never half written.
func replace(p contract.Platform, path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return p.Replace(tmp.Name(), path)
}

// mac signs words for one purpose, so that what is signed for one is of no
// use for another.
func mac(key []byte, purpose string, words ...string) string {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(purpose + "\x00" + strings.Join(words, "\x00")))
	return b64.EncodeToString(h.Sum(nil))
}

func same(a, b string) bool { return hmac.Equal([]byte(a), []byte(b)) }

// newCode is a code for one purpose: the moment it was made, signed. The
// moment is its number, so a later code always has a higher one.
func newCode(key []byte, purpose string, now time.Time) string {
	n := strconv.FormatInt(now.UnixNano(), 36)
	return n + "." + mac(key, purpose, n)
}

// codeNumber is the number of a code that is rightly signed and not older
// than a minute, and 0 for any other.
func codeNumber(key []byte, purpose, code string, now time.Time) int64 {
	n, sig, _ := strings.Cut(code, ".")
	made, err := strconv.ParseInt(n, 36, 64)
	if age := now.Sub(time.Unix(0, made)); err != nil || !same(sig, mac(key, purpose, n)) || age < 0 || age > codeLife {
		return 0
	}
	return made
}

// signIn takes a sign-in code once. The number of the last one used is in a
// file, so a used code stays used when the server starts again, and so does
// every code made before it.
func (s *server) signIn(code string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fails >= slowFrom && time.Since(s.failed) < time.Second<<min(s.fails-slowFrom, slowMost) {
		return errSlow
	}
	path := filepath.Join(s.dirs.State, countFile)
	n := codeNumber(s.key, "in", code, now)
	data, err := s.k.Platform.Read(path)
	last, _ := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if n == 0 || n <= last || err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.fails, s.failed = s.fails+1, time.Now()
		return errCode
	}
	s.fails = 0
	return replace(s.k.Platform, path, []byte(strconv.FormatInt(n, 10)+"\n"))
}

var (
	errCode = errors.New("that sign-in code is wrong, used or older than a minute")
	errSlow = errors.New("too many wrong sign-in codes: wait a little")
)

// grant sets the session cookie: a random name for the session and its end,
// signed. Scripts cannot read it and no other site's request carries it.
func (s *server) grant(w http.ResponseWriter, id string, now time.Time) {
	end := strconv.FormatInt(now.Add(sessionLife).Unix(), 36)
	http.SetCookie(w, &http.Cookie{Name: s.cookie, Value: id + "." + end + "." + mac(s.key, "session", id, end),
		Path: "/", MaxAge: int(sessionLife / time.Second), HttpOnly: true, SameSite: http.SameSiteStrictMode})
}

// session is the session a request's cookie names, renewed by use; ok is
// false for none, a forged one and one that has run out.
func (s *server) session(w http.ResponseWriter, r *http.Request, now time.Time) (id string, ok bool) {
	c, err := r.Cookie(s.cookie)
	if err != nil {
		return "", false
	}
	part := strings.Split(c.Value, ".")
	if len(part) != 3 || !same(part[2], mac(s.key, "session", part[0], part[1])) {
		return "", false
	}
	end, _ := strconv.ParseInt(part[1], 36, 64)
	left := time.Unix(end, 0).Sub(now)
	if left <= 0 {
		return "", false
	}
	if left < sessionLife-renewAfter {
		s.grant(w, part[0], now)
	}
	return part[0], true
}
