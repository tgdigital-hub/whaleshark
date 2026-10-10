// Package push is the page's own nudge: one sealed message for each paired
// phone that asked for nudges, sent to that phone maker's push service and
// to no other address, with the standard library alone. It binds no command
// and does not decide whether a nudge is due: Mute, Do not disturb and
// nudge.phone are the notifier's, which calls Push last. What a phone hands
// over is stored by the page's server.
package push

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// makers are the push services of the makers of phones and their browsers:
// Apple's, Google's, Mozilla's and Microsoft's. A name with a dot in front
// stands for every name that ends so. An address anywhere else is never
// called, whatever a browser handed over.
var makers = []string{".push.apple.com", "fcm.googleapis.com",
	"updates.push.services.mozilla.com", ".notify.windows.com"}

// client waits ten seconds for a push service and follows it nowhere else.
var client = &http.Client{Timeout: 10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// sender is the kit's Push and PushKey. do and fresh are the two things a
// test plays: the request, and what a message uses once.
type sender struct {
	k     *contract.Kit
	do    func(*http.Request) (*http.Response, error)
	fresh func() (once, error)
}

// Plug puts the phone's nudge into the kit.
func Plug(k *contract.Kit) {
	s := &sender{k, client.Do, fresh}
	k.Push, k.PushKey = s.push, s.public
}

// keyFile is push.key in the settings folder: the private half of the
// login's key for nudges, a number on the curve P-256.
type keyFile struct {
	contract.Versioned
	Key string `json:"key"`
}

// key returns the login's key for nudges and makes it at first use, under a
// lock so that two first uses end with one key.
func (s *sender) key() (*ecdsa.PrivateKey, error) {
	p := s.k.Platform
	dirs, err := p.Dirs()
	if err == nil {
		err = os.MkdirAll(dirs.State, 0o700)
	}
	if err != nil {
		return nil, err
	}
	unlock, err := p.Lock(filepath.Join(dirs.State, "push.lock"), true)
	if err != nil {
		return nil, err
	}
	defer unlock()
	path, f := filepath.Join(dirs.Config, "push.key"), keyFile{}
	if err := contract.ReadVersioned(p.Read, path, contract.FileVersion, &f); err != nil {
		return nil, err
	}
	if f.Key == "" {
		made, err := ecdh.P256().GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		f = keyFile{contract.Versioned{Version: contract.FileVersion}, b64.EncodeToString(made.Bytes())}
		if err := contract.WriteVersioned(p, path, contract.FileVersion, f); err != nil {
			return nil, err
		}
	}
	raw, err := b64.DecodeString(f.Key)
	if err != nil {
		return nil, err
	}
	return ecdsa.ParseRawPrivateKey(elliptic.P256(), raw)
}

// public is the kit's PushKey: the public half as a browser wants it, or
// nothing when there is no key to be had.
func (s *sender) public() string {
	key, err := s.key()
	if err != nil {
		return ""
	}
	point, _ := key.PublicKey.Bytes()
	return b64.EncodeToString(point)
}

// message is what the page's script on the phone is woken with. Tag is the
// task, so that a second notice about it takes the first one's place.
type message struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	Link  string `json:"link,omitempty"`
	Tag   string `json:"tag,omitempty"`
}

// push is the kit's Push. A phone that is reached counts; a phone its
// service says is gone loses what it handed over and is no error.
func (s *sender) push(n contract.Nudge, link string) (int, error) {
	dirs, err := s.k.Platform.Dirs()
	if err != nil {
		return 0, err
	}
	phones, err := contract.ReadPhones(s.k.Platform.Peek, dirs.State)
	if err != nil {
		return 0, err
	}
	phones.Paired = slices.DeleteFunc(phones.Paired, func(p contract.Phone) bool { return p.Push == nil })
	if len(phones.Paired) == 0 {
		return 0, nil
	}
	key, err := s.key()
	if err != nil {
		return 0, err
	}
	m := message{n.Title, n.Body, link, n.Task}
	plain, _ := json.Marshal(m)
	for len(plain) > room && m.Body != "" { // the text gives way, never the link
		m.Body = strings.ToValidUTF8(m.Body[:len(m.Body)/2], "")
		plain, _ = json.Marshal(m)
	}
	var (
		wg   sync.WaitGroup
		errs = make([]error, len(phones.Paired))
		gone = make([]bool, len(phones.Paired))
	)
	for i, p := range phones.Paired {
		wg.Go(func() {
			if gone[i], errs[i] = s.send(key, p.Push, plain, n.Urgent); errs[i] != nil {
				errs[i] = fmt.Errorf("phone %s: %w", p.Name, errs[i])
			}
		})
	}
	wg.Wait()
	sent := 0
	for i := range errs {
		if errs[i] == nil && !gone[i] {
			sent++
		}
	}
	if slices.Contains(gone, true) {
		errs = append(errs, contract.ChangePhones(s.k.Platform, func(list *contract.Phones) error {
			for i, p := range phones.Paired {
				for j := range list.Paired {
					if at := &list.Paired[j]; gone[i] && at.ID == p.ID && at.Push != nil && *at.Push == *p.Push {
						at.Push = nil
					}
				}
			}
			return nil
		}))
	}
	return sent, errors.Join(errs...)
}

// send is one request to the push service of one phone (RFC 8030, section
// 5). gone means the service knows the phone's address no longer.
func (s *sender) send(key *ecdsa.PrivateKey, to *contract.PushTo, plain []byte, urgent bool) (gone bool, err error) {
	u, err := url.Parse(to.Endpoint)
	if err != nil || u.Scheme != "https" || u.Port() != "" || !slices.ContainsFunc(makers, func(m string) bool {
		return u.Host == m || m[0] == '.' && strings.HasSuffix(u.Host, m)
	}) {
		return false, errors.New("its address is at no phone maker's push service; nothing was sent")
	}
	o, err := s.fresh()
	if err != nil {
		return false, err
	}
	body, err := seal(to, plain, o)
	if err != nil {
		return false, err
	}
	auth, err := word(key, "https://"+u.Host, time.Now())
	if err != nil {
		return false, err
	}
	req, err := http.NewRequest(http.MethodPost, to.Endpoint, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	urgency := "normal"
	if urgent {
		urgency = "high"
	}
	for name, value := range map[string]string{"Authorization": auth, "Content-Encoding": "aes128gcm",
		"Content-Type": "application/octet-stream", "TTL": "86400", "Urgency": urgency} {
		req.Header.Set(name, value)
	}
	res, err := s.do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // the address is the phone's secret and stays out of any message
		}
		return false, err
	}
	res.Body.Close()
	switch {
	case res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone:
		return true, nil
	case res.StatusCode >= 300:
		return false, fmt.Errorf("its push service answered %s", res.Status)
	}
	return false, nil
}
