// Package webpushtest plays the browser side of Web Push in tests: it creates
// subscriptions and decrypts what a sender posted to them.
package webpushtest

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

var b64 = base64.RawURLEncoding

// Subscriber holds a user agent key pair and auth secret.
type Subscriber struct {
	Private    *ecdh.PrivateKey
	AuthSecret []byte
}

// NewSubscriber creates fresh keys.
func NewSubscriber() *Subscriber {
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		panic(err)
	}
	return &Subscriber{Private: priv, AuthSecret: auth}
}

// P256dh is the base64url public key a browser reports.
func (s *Subscriber) P256dh() string { return b64.EncodeToString(s.Private.PublicKey().Bytes()) }

// Auth is the base64url auth secret a browser reports.
func (s *Subscriber) Auth() string { return b64.EncodeToString(s.AuthSecret) }

// Decrypt reverses RFC 8291 aes128gcm encryption of a single-record body.
func (s *Subscriber) Decrypt(body []byte) ([]byte, error) {
	if len(body) < 21 {
		return nil, errors.New("body too short")
	}
	salt := body[:16]
	rs := binary.BigEndian.Uint32(body[16:20])
	idLen := int(body[20])
	if len(body) < 21+idLen {
		return nil, errors.New("body too short for key id")
	}
	asPublicRaw := body[21 : 21+idLen]
	ciphertext := body[21+idLen:]
	if uint32(len(ciphertext)) > rs {
		return nil, fmt.Errorf("record of %d bytes exceeds record size %d", len(ciphertext), rs)
	}

	asPublic, err := ecdh.P256().NewPublicKey(asPublicRaw)
	if err != nil {
		return nil, err
	}
	secret, err := s.Private.ECDH(asPublic)
	if err != nil {
		return nil, err
	}
	uaPublicRaw := s.Private.PublicKey().Bytes()
	info := append([]byte("WebPush: info\x00"), uaPublicRaw...)
	info = append(info, asPublicRaw...)
	ikm, err := hkdf.Key(sha256.New, secret, s.AuthSecret, string(info), 32)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	plain = bytes.TrimRight(plain, "\x00")
	if len(plain) == 0 || plain[len(plain)-1] != 0x02 {
		return nil, errors.New("missing last-record delimiter")
	}
	return plain[:len(plain)-1], nil
}

// ParseAuthorization splits a "vapid t=<jwt>, k=<key>" header.
func ParseAuthorization(header string) (token, key string, err error) {
	rest, ok := strings.CutPrefix(header, "vapid ")
	if !ok {
		return "", "", errors.New("not a vapid authorization")
	}
	for part := range strings.SplitSeq(rest, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch k {
		case "t":
			token = v
		case "k":
			key = v
		}
	}
	if token == "" || key == "" {
		return "", "", errors.New("vapid authorization missing t or k")
	}
	return token, key, nil
}
