package dash

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// The Cloudflare road's door. Cloudflare's login puts a signed pass on every
// request it lets through; the door takes nobody's word for it and checks
// each one itself, as the standards for such a pass describe it (a JSON Web
// Token, RFC 7519, signed as RFC 7515 says, with keys published as RFC 7517
// says). Whatever fails gets an answer with nothing in it.
const (
	certs = "/cdn-cgi/access/certs"
	// The keys are asked for again when a pass names one that is not held,
	// but not more often than keysEvery, and whatever a pass names once they
	// are keysLife old, so that a key Cloudflare took back is not held for as
	// long as the server runs. skew is the allowance for two clocks that
	// disagree.
	keysEvery = time.Minute
	keysLife  = 24 * time.Hour
	skew      = time.Minute
	// More than refusalsMost requests without a valid pass in a minute shut
	// the door for shutFor, to all but a pass that holds, and the person is
	// told for contract.ShutShown.
	refusalsMost = 20
	shutFor      = time.Minute
)

// fetch asks Cloudflare for a team's keys: the page's one call out, to the
// address the administrator wrote into the server's file.
var fetch = func(address string) ([]byte, error) {
	web := http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := web.Get(address) // #nosec G107 G704 -- the team's own address at Cloudflare, from the server's file, which is root's
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBody))
}

// guard is what the door keeps for the Cloudflare road: the team's keys by
// their ids and when they were last asked for; the refusals of the minute
// that began at minute, and when the door last shut itself; why the last
// request was refused, and the shape of the last pass whose signature held.
type guard struct {
	asking sync.Mutex
	issuer string
	asked  time.Time
	keys   map[string]*rsa.PublicKey

	turned       int
	minute, shut time.Time
	why, shape   string
}

// find is the team's key of that id, nil where Cloudflare publishes none or
// cannot be asked.
func (g *guard) find(issuer, id string, now time.Time) *rsa.PublicKey {
	g.asking.Lock()
	defer g.asking.Unlock()
	if g.issuer != issuer {
		g.issuer, g.asked, g.keys = issuer, time.Time{}, nil
	}
	old := now.Sub(g.asked) >= keysLife
	if g.keys[id] != nil && !old || now.Sub(g.asked) < keysEvery {
		return g.keys[id]
	}
	g.asked = now
	var set struct {
		Keys []struct{ Kid, Kty, N, E string }
	}
	if data, err := fetch(issuer + certs); err != nil || json.Unmarshal(data, &set) != nil {
		if old {
			g.keys = nil // a day without Cloudflare's word: no key is held on to
		}
		return g.keys[id]
	}
	g.keys = map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		n, nerr := b64.DecodeString(k.N)
		e, eerr := b64.DecodeString(k.E)
		mod, exp := new(big.Int).SetBytes(n), 0
		for _, b := range e {
			exp = exp<<8 | int(b)
		}
		if k.Kty == "RSA" && k.Kid != "" && nerr == nil && eerr == nil && mod.BitLen() >= 2048 && len(e) <= 4 && exp > 1 {
			g.keys[k.Kid] = &rsa.PublicKey{N: mod, E: exp}
		}
	}
	return g.keys[id]
}

// words is a field that holds one word or a list of them.
type words []string

func (w *words) UnmarshalJSON(data []byte) error {
	var one string
	if json.Unmarshal(data, &one) == nil {
		*w = words{one}
		return nil
	}
	return json.Unmarshal(data, (*[]string)(w))
}

// check holds one request against a person's values. why is empty for a
// pass that is Cloudflare's, for this person's own application and name, in
// its time; who is then the subject it names with the address, and until
// when it runs out. shape is a signed pass's heading and the names of its
// fields, never a value of them.
func (g *guard) check(r *http.Request, a contract.Access, now time.Time) (who string, until time.Time, shape, why string) {
	part := strings.Split(r.Header.Get(contract.HeaderAccess), ".")
	var head struct{ Alg, Kid string }
	var c struct {
		Iss, Type, Email, Sub string
		Aud                   words
		Exp, Nbf, Iat         int64
	}
	var raw [3][]byte
	var err error
	for i := 0; i < len(raw) && len(part) == len(raw) && err == nil; i++ {
		raw[i], err = b64.DecodeString(part[i])
	}
	switch {
	case !strings.EqualFold(r.Host, a.Name):
		return who, until, "", "another host name"
	case part[0] == "":
		return who, until, "", "no pass"
	case len(part) != len(raw) || err != nil || json.Unmarshal(raw[0], &head) != nil:
		return who, until, "", "not a pass"
	case head.Alg != "RS256":
		// Before the signature is looked at: no other way of signing, and
		// "none" least of all, is ever tried.
		return who, until, "", "another way of signing"
	}
	key, sum := g.find(a.Issuer(), head.Kid, now), sha256.Sum256([]byte(part[0]+"."+part[1]))
	switch {
	case key == nil:
		return who, until, "", "a key Cloudflare does not publish, or its keys cannot be fetched"
	case rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], raw[2]) != nil:
		return who, until, "", "a wrong signature"
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(raw[1], &fields)
	shape = string(raw[0]) + " " + strings.Join(slices.Sorted(maps.Keys(fields)), " ")
	late := func(unix int64) bool { return time.Unix(unix, 0).After(now.Add(skew)) }
	if json.Unmarshal(raw[1], &c) != nil {
		return who, until, shape, "fields that cannot be read"
	}
	switch until = time.Unix(c.Exp, 0).Add(skew); {
	case c.Iss != a.Issuer():
		why = "another issuer"
	case !slices.Contains(c.Aud, a.Aud):
		why = "another application's tag"
	case c.Type != "app":
		why = "not an application's pass"
	case c.Exp == 0 || !now.Before(until):
		why = "run out"
	case late(c.Nbf) || late(c.Iat):
		why = "from the future"
	case c.Sub == "" || !slices.ContainsFunc(a.Email, func(mine string) bool { return strings.EqualFold(mine, c.Email) }):
		why = "an address that is not this person's"
	}
	return c.Email + " " + c.Sub, until, shape, why
}

// pass is the Cloudflare road's check of one request, before anything else
// is read of it. Without this login's values and without the keys nothing
// passes: there is no way round the check. While the door is shut a request
// is checked as ever and one whose pass holds still passes, so that nobody
// keeps the person out by sending refusals; the rest are refused uncounted.
func (s *server) pass(r *http.Request, srv *contract.Server, now time.Time) (who string, until time.Time, ok bool) {
	shape, why := "", "this login has no values for Cloudflare's road in the server's file"
	if a, known := srv.Access(s.login); known {
		who, until, shape, why = s.guard.check(r, a, now)
	}
	s.mu.Lock()
	shut, shuts := !s.shut.IsZero() && now.Sub(s.shut) < shutFor, false
	if shape != "" {
		s.shape = shape
	}
	if why != "" && !shut {
		if s.why = why; now.Sub(s.minute) >= time.Minute {
			s.minute, s.turned = now, 0
		}
		if s.turned++; s.turned > refusalsMost {
			s.shut, s.turned, shuts = now, 0, true
		}
	}
	s.mu.Unlock()
	if shuts {
		s.shutNow(now)
		s.wake()
	}
	return who, until, why == ""
}
