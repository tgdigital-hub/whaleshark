package push

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Written from the public standards and nothing else: RFC 8291 (message
// encryption for web push), RFC 8188 (the aes128gcm content coding), RFC
// 5869 (HKDF), RFC 8292 (the sender's signed word, VAPID), RFC 7515 and
// 7518 (the signature's form, ES256) and RFC 8030 (the request itself).

// record is the one record a message is, header and all, which every push
// service takes; room is what of it is left for the text: 86 bytes are the
// header, 16 the seal and one the mark that ends it.
const (
	record = 4096
	room   = record - 86 - 16 - 1
)

var b64 = base64.RawURLEncoding

// once is what a message uses once and never again: the sender's half of
// the key agreement and the salt.
type once struct {
	key  *ecdh.PrivateKey
	salt []byte
}

func fresh() (o once, err error) {
	o.salt = make([]byte, 16)
	rand.Read(o.salt)
	o.key, err = ecdh.P256().GenerateKey(rand.Reader)
	return o, err
}

// seal is plain as only the phone that to came from can read it: the body
// of the request, in the aes128gcm coding with the sender's public key as
// the key id (RFC 8291, section 4).
func seal(to *contract.PushTo, plain []byte, o once) ([]byte, error) {
	if len(plain) > room {
		return nil, errors.New("the nudge is longer than one push message holds")
	}
	point, err := b64.DecodeString(to.P256dh)
	if err != nil {
		return nil, err
	}
	auth, err := b64.DecodeString(to.Auth)
	if err != nil {
		return nil, err
	}
	phone, err := ecdh.P256().NewPublicKey(point)
	if err != nil {
		return nil, err
	}
	shared, err := o.key.ECDH(phone)
	if err != nil {
		return nil, err
	}
	mine := o.key.PublicKey().Bytes()
	derive := func(secret, salt []byte, info string, n int) []byte {
		out, _ := hkdf.Key(sha256.New, secret, salt, info, n) // fails only for a length no hash gives
		return out
	}
	ikm := derive(shared, auth, "WebPush: info\x00"+string(point)+string(mine), 32)
	block, err := aes.NewCipher(derive(ikm, o.salt, "Content-Encoding: aes128gcm\x00", 16))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	body := binary.BigEndian.AppendUint32(append([]byte{}, o.salt...), record)
	body = append(append(body, byte(len(mine))), mine...)
	return gcm.Seal(body, derive(ikm, o.salt, "Content-Encoding: nonce\x00", 12), slices.Concat(plain, []byte{2}), nil), nil
}

// contact is whom a push service is told to write to about our messages.
// It is the project's public address, the same for every login: a person's
// own address or the name of their page is nothing a push service needs.
const contact = "https://github.com/tgdigital-hub/whaleshark"

// word is the Authorization line of a request to the push service at
// origin: a statement signed with the login's key that the sender is the
// one the phone subscribed to, good for twelve hours (RFC 8292, section 3).
func word(key *ecdsa.PrivateKey, origin string, now time.Time) (string, error) {
	public, err := key.PublicKey.Bytes()
	if err != nil {
		return "", err
	}
	claims, _ := json.Marshal(map[string]any{"aud": origin, "exp": now.Add(12 * time.Hour).Unix(), "sub": contact})
	text := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`)) + "." + b64.EncodeToString(claims)
	sum := sha256.Sum256([]byte(text))
	r, s, err := ecdsa.Sign(rand.Reader, key, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return "vapid t=" + text + "." + b64.EncodeToString(sig) + ", k=" + b64.EncodeToString(public), nil
}
