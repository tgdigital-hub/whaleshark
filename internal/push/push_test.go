package push

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
)

// The example of RFC 8291, section 5 and appendix A: what the phone handed
// over, the phone's own private key, what the sender used once, the text
// and the message that must come of them.
const (
	exPhone   = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	exPhoneD  = "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"
	exAuth    = "BTBZMqHH6r4Tts7J_aSIgg"
	exSenderD = "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"
	exSalt    = "DGv6ra1nlYgDCS1FRnbzlw"
	exText    = "When I grow up, I want to be a watermelon"
	exMessage = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_" +
		"yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
)

func raw(t *testing.T, s string) []byte {
	t.Helper()
	b, err := b64.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type heard struct {
	host, path, body string
	header           http.Header
}

// desk is a pretended login with a push service of the test's own: every
// request is played to a listener on this machine, which records it and
// answers 201, or what the path's last word says.
type desk struct {
	*testing.T
	k     *contract.Kit
	s     *sender
	mu    sync.Mutex
	heard []heard
}

func newDesk(t *testing.T) *desk {
	home := t.TempDir()
	for _, name := range []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(name, filepath.Join(home, name))
	}
	for _, name := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(name, "")
	}
	d := &desk{T: t, k: contract.NewKit()}
	platform.Plug(d.k)
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		d.mu.Lock()
		d.heard = append(d.heard, heard{r.Header.Get("X-Asked"), r.URL.Path, string(body), r.Header})
		d.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/gone"):
			w.WriteHeader(http.StatusGone)
		case strings.HasSuffix(r.URL.Path, "/unknown"):
			w.WriteHeader(http.StatusNotFound)
		case strings.HasSuffix(r.URL.Path, "/busy"):
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			w.WriteHeader(http.StatusCreated)
		}
	}))
	t.Cleanup(service.Close)
	d.s = &sender{d.k, func(r *http.Request) (*http.Response, error) {
		r.Header.Set("X-Asked", r.URL.Scheme+"://"+r.URL.Host)
		r.URL.Scheme, r.URL.Host = "http", service.Listener.Addr().String()
		return http.DefaultClient.Do(r)
	}, func() (once, error) {
		key, err := ecdh.P256().NewPrivateKey(raw(t, exSenderD))
		return once{key, raw(t, exSalt)}, err
	}}
	d.k.Push, d.k.PushKey = d.s.push, d.s.public
	return d
}

// pair writes the login's phones: one per address, each with the example's
// keys; an empty address is a phone that never asked for nudges.
func (d *desk) pair(addresses ...string) {
	d.Helper()
	err := contract.ChangePhones(d.k.Platform, func(list *contract.Phones) error {
		list.Paired = nil
		for i, a := range addresses {
			p := contract.Phone{ID: "ph" + string(rune('1'+i)), Name: "phone " + string(rune('1'+i)), Session: "x"}
			if a != "" {
				p.Push = &contract.PushTo{Endpoint: a, P256dh: exPhone, Auth: exAuth}
			}
			list.Paired = append(list.Paired, p)
		}
		return nil
	})
	if err != nil {
		d.Fatal(err)
	}
}

func (d *desk) phones() []contract.Phone {
	d.Helper()
	dirs, _ := d.k.Platform.Dirs()
	list, err := contract.ReadPhones(d.k.Platform.Read, dirs.State)
	if err != nil {
		d.Fatal(err)
	}
	return list.Paired
}

// open reads a message as the example's phone does, written apart from
// seal: the header's fields by their places, then the two keys.
func open(t *testing.T, body []byte) string {
	t.Helper()
	salt, sender, sealed := body[:16], body[21:86], body[86:]
	mine, err := ecdh.P256().NewPrivateKey(raw(t, exPhoneD))
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := ecdh.P256().NewPublicKey(sender)
	if err != nil {
		t.Fatal(err)
	}
	shared, _ := mine.ECDH(theirs)
	prk, _ := hkdf.Extract(sha256.New, shared, raw(t, exAuth))
	ikm, _ := hkdf.Expand(sha256.New, prk, "WebPush: info\x00"+string(mine.PublicKey().Bytes())+string(sender), 32)
	prk, _ = hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plain[len(plain)-1] != 2 {
		t.Fatalf("the record ends with %d, not the mark of a last record", plain[len(plain)-1])
	}
	return string(plain[:len(plain)-1])
}

func TestARecordedExchangeIsTheDocumentedMessage(t *testing.T) {
	d := newDesk(t)
	key, err := d.s.key()
	if err != nil {
		t.Fatal(err)
	}
	to := &contract.PushTo{Endpoint: "https://fcm.googleapis.com/fcm/send/example", P256dh: exPhone, Auth: exAuth}
	if gone, err := d.s.send(key, to, []byte(exText), false); gone || err != nil {
		t.Fatal(gone, err)
	}
	got := d.heard[0]
	if b64.EncodeToString([]byte(got.body)) != exMessage {
		t.Fatalf("the message is not the standard's:\n got %s\nwant %s", b64.EncodeToString([]byte(got.body)), exMessage)
	}
	if open(t, []byte(got.body)) != exText {
		t.Fatal("the test's own reader does not read the standard's message")
	}
	if got.host != "https://fcm.googleapis.com" || got.path != "/fcm/send/example" {
		t.Fatalf("asked %s %s", got.host, got.path)
	}
	for name, want := range map[string]string{"Content-Encoding": "aes128gcm", "Ttl": "86400", "Urgency": "normal",
		"Content-Type": "application/octet-stream", "Content-Length": "144"} {
		if got.header.Get(name) != want {
			t.Errorf("%s is %q, want %q", name, got.header.Get(name), want)
		}
	}
}

// The sender's word is read as a push service reads it (RFC 8292): signed
// by the key the page hands the phone, for this service, for a while.
func TestTheWordIsSignedWithTheLoginsKey(t *testing.T) {
	d := newDesk(t)
	d.pair("https://web.push.apple.com/one")
	public := d.k.PushKey()
	if n, err := d.k.Push(contract.Nudge{Title: "T1 waits for you", Urgent: true}, ""); n != 1 || err != nil {
		t.Fatal(n, err)
	}
	if again := d.k.PushKey(); again != public || len(raw(t, public)) != 65 {
		t.Fatalf("the key changed or is no point: %q then %q", public, again)
	}
	h := d.heard[0].header
	if h.Get("Urgency") != "high" {
		t.Errorf("urgency %q", h.Get("Urgency"))
	}
	token, k, ok := strings.Cut(strings.TrimPrefix(h.Get("Authorization"), "vapid t="), ", k=")
	if !ok || k != public {
		t.Fatalf("authorization %q", h.Get("Authorization"))
	}
	part := strings.Split(token, ".")
	if len(part) != 3 || string(raw(t, part[0])) != `{"typ":"JWT","alg":"ES256"}` {
		t.Fatalf("token %q", token)
	}
	var claims struct {
		Aud, Sub string
		Exp      int64
	}
	if err := json.Unmarshal(raw(t, part[1]), &claims); err != nil {
		t.Fatal(err)
	}
	left := time.Until(time.Unix(claims.Exp, 0))
	if claims.Aud != "https://web.push.apple.com" || !strings.HasPrefix(claims.Sub, "https://") || left <= 0 || left > 24*time.Hour {
		t.Fatalf("claims %+v", claims)
	}
	point, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), raw(t, k))
	if err != nil {
		t.Fatal(err)
	}
	sig, sum := raw(t, part[2]), sha256.Sum256([]byte(part[0]+"."+part[1]))
	if len(sig) != 64 || !ecdsa.Verify(point, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("the signature does not hold")
	}
	dirs, _ := d.k.Platform.Dirs()
	file := filepath.Join(dirs.Config, "push.key")
	if info, err := os.Stat(file); err != nil || d.k.Platform.System() != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("push.key: %v %v", info, err)
	}
	if err := os.WriteFile(file, []byte(`{"version":99,"key":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.k.Push(contract.Nudge{Title: "x"}, ""); err == nil || d.k.PushKey() != "" || len(d.heard) != 1 {
		t.Fatalf("a newer key file was used: %v, %d requests", err, len(d.heard))
	}
}

func TestANudgeArrivesAsThePageReadsIt(t *testing.T) {
	d := newDesk(t)
	d.pair("https://fcm.googleapis.com/a", "https://updates.push.services.mozilla.com/b")
	long := strings.Repeat("wörk <done> ", 600)
	n, err := d.k.Push(contract.Nudge{Title: "T2 asks a question", Body: long, Task: "T2"}, "https://page.example/item/7")
	if n != 2 || err != nil || len(d.heard) != 2 {
		t.Fatal(n, err, len(d.heard))
	}
	for _, h := range d.heard {
		if len(h.body) > record {
			t.Fatalf("a message of %d bytes", len(h.body))
		}
		var m message
		if err := json.Unmarshal([]byte(open(t, []byte(h.body))), &m); err != nil {
			t.Fatal(err)
		}
		if m.Title != "T2 asks a question" || m.Link != "https://page.example/item/7" || m.Tag != "T2" ||
			m.Body == "" || !strings.HasPrefix(long, m.Body) {
			t.Fatalf("read %+v", m)
		}
	}
}

func TestAPhoneTheServiceSaysIsGoneIsDropped(t *testing.T) {
	d := newDesk(t)
	d.pair("https://fcm.googleapis.com/gone", "https://fcm.googleapis.com/stays", "https://web.push.apple.com/unknown",
		"https://fcm.googleapis.com/busy")
	n, err := d.k.Push(contract.Nudge{Title: "x"}, "")
	if n != 1 || err == nil || !strings.Contains(err.Error(), "phone 4") || strings.Contains(err.Error(), "googleapis") {
		t.Fatal(n, err)
	}
	left := d.phones()
	if len(left) != 4 || left[0].Push != nil || left[1].Push == nil || left[2].Push != nil || left[3].Push == nil {
		t.Fatalf("after gone: %+v", left)
	}
	d.heard = nil
	if n, _ := d.k.Push(contract.Nudge{Title: "x"}, ""); n != 1 || len(d.heard) != 2 {
		t.Fatalf("after the drop %d reached, %d asked", n, len(d.heard))
	}
}

func TestNothingIsSentForAPhoneThatIsNotPaired(t *testing.T) {
	d := newDesk(t)
	if n, err := d.k.Push(contract.Nudge{Title: "x"}, ""); n != 0 || err != nil {
		t.Fatal("no phones at all:", n, err)
	}
	d.pair("", "")
	if n, err := d.k.Push(contract.Nudge{Title: "x"}, ""); n != 0 || err != nil {
		t.Fatal("phones that never asked:", n, err)
	}
	dirs, _ := d.k.Platform.Dirs()
	if _, err := os.Stat(filepath.Join(dirs.Config, "push.key")); err == nil {
		t.Error("a key was made with nobody to nudge")
	}
	d.pair("https://fcm.googleapis.com/a")
	if n, err := d.k.Push(contract.Nudge{Title: "x"}, ""); n != 1 || err != nil {
		t.Fatal(n, err)
	}
	d.pair() // forgotten
	if n, err := d.k.Push(contract.Nudge{Title: "x"}, ""); n != 0 || err != nil || len(d.heard) != 1 {
		t.Fatal("a forgotten phone:", n, err, len(d.heard))
	}
	// An address that is no phone maker's push service is never called.
	d.pair("https://push.example/a", "http://fcm.googleapis.com/a", "https://fcm.googleapis.com:8443/a",
		"https://fcm.googleapis.com.example/a", "https://notapush.apple.com/a", "https://localhost/a")
	if n, err := d.k.Push(contract.Nudge{Title: "x"}, ""); n != 0 || err == nil || len(d.heard) != 1 {
		t.Fatal("an address elsewhere:", n, err, len(d.heard))
	}
}
