// Package webpush sends Web Push messages (RFC 8030) with payload encryption
// (RFC 8291, aes128gcm) and VAPID authentication (RFC 8292), using only the
// standard library.
package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// MaxPayload is the largest plaintext that fits in the 4096-byte record push
// services must accept, after the 86-byte header, the padding delimiter and
// the 16-byte GCM tag.
const MaxPayload = 4096 - 86 - 1 - 16

const recordSize = 4096

var b64 = base64.RawURLEncoding

// Keys is a VAPID key pair. Both halves use the encoding browsers and other
// Web Push libraries expect: the public key is the 65-byte uncompressed P-256
// point and the private key is the 32-byte scalar, each base64url without
// padding.
type Keys struct {
	Public  string
	Private string
	signer  *ecdsa.PrivateKey
}

// GenerateKeys creates a new VAPID key pair.
func GenerateKeys() (*Keys, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate vapid key: %w", err)
	}
	raw, err := priv.Bytes()
	if err != nil {
		return nil, fmt.Errorf("encode vapid key: %w", err)
	}
	return ParseKeys(b64.EncodeToString(raw))
}

// ParseKeys loads a VAPID key pair from its base64url private scalar and
// derives the public key from it.
func ParseKeys(private string) (*Keys, error) {
	raw, err := decodeB64(private)
	if err != nil {
		return nil, fmt.Errorf("vapid private key: %w", err)
	}
	priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), raw)
	if err != nil {
		return nil, fmt.Errorf("vapid private key: %w", err)
	}
	pub, err := priv.PublicKey.Bytes()
	if err != nil {
		return nil, fmt.Errorf("vapid public key: %w", err)
	}
	return &Keys{Public: b64.EncodeToString(pub), Private: b64.EncodeToString(raw), signer: priv}, nil
}

// Subscription is what the browser's PushManager.subscribe() returns.
type Subscription struct {
	Endpoint string
	P256dh   string // base64url user agent public key
	Auth     string // base64url 16-byte auth secret
}

// ValidateSubscription checks the parts a sender relies on. The endpoint
// must be https: push services never use plain http, and allowing it would
// turn subscriptions into a way to make the server send requests anywhere.
func ValidateSubscription(s Subscription) error {
	u, err := url.Parse(s.Endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return errors.New("endpoint must be an https URL")
	}
	if len(s.Endpoint) > 2048 {
		return errors.New("endpoint is too long")
	}
	pub, err := decodeB64(s.P256dh)
	if err != nil {
		return errors.New("p256dh must be base64url")
	}
	if _, err := ecdh.P256().NewPublicKey(pub); err != nil {
		return errors.New("p256dh is not a P-256 public key")
	}
	auth, err := decodeB64(s.Auth)
	if err != nil || len(auth) != 16 {
		return errors.New("auth must be 16 bytes of base64url")
	}
	return nil
}

// Message is one push delivery.
type Message struct {
	Payload []byte
	// TTL is how long the push service keeps the message for an offline
	// device. Zero means deliver now or drop.
	TTL time.Duration
	// Urgency is "very-low", "low", "normal" or "high". Empty means normal.
	Urgency string
	// Topic lets a newer message replace an undelivered older one with the
	// same topic. At most 32 base64url characters.
	Topic string
}

// Sender delivers messages with one VAPID identity.
type Sender struct {
	Keys    *Keys
	Subject string // mailto: or https: contact for the push service operator
	Client  *http.Client
	now     func() time.Time
	random  io.Reader
}

// Result is the push service's answer.
type Result struct {
	StatusCode int
	// Gone is true when the subscription no longer exists (404 or 410) and
	// should be deleted.
	Gone bool
}

// Send encrypts and posts one message. A non-2xx answer is returned as an
// error together with the Result so callers can still inspect Gone.
func (s *Sender) Send(ctx context.Context, sub Subscription, msg Message) (Result, error) {
	if len(msg.Payload) > MaxPayload {
		return Result{}, fmt.Errorf("payload is %d bytes, limit is %d", len(msg.Payload), MaxPayload)
	}
	endpoint, err := url.Parse(sub.Endpoint)
	if err != nil {
		return Result{}, fmt.Errorf("endpoint: %w", err)
	}
	uaPublic, err := decodeB64(sub.P256dh)
	if err != nil {
		return Result{}, fmt.Errorf("p256dh: %w", err)
	}
	authSecret, err := decodeB64(sub.Auth)
	if err != nil {
		return Result{}, fmt.Errorf("auth: %w", err)
	}

	random := s.random
	if random == nil {
		random = rand.Reader
	}
	asPrivate, err := ecdh.P256().GenerateKey(random)
	if err != nil {
		return Result{}, fmt.Errorf("ephemeral key: %w", err)
	}
	salt := make([]byte, 16)
	if _, err := io.ReadFull(random, salt); err != nil {
		return Result{}, fmt.Errorf("salt: %w", err)
	}
	body, err := encrypt(msg.Payload, asPrivate, uaPublic, authSecret, salt)
	if err != nil {
		return Result{}, err
	}

	token, err := s.vapidToken(endpoint.Scheme + "://" + endpoint.Host)
	if err != nil {
		return Result{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", strconv.Itoa(int(msg.TTL/time.Second)))
	req.Header.Set("Authorization", "vapid t="+token+", k="+s.Keys.Public)
	if msg.Urgency != "" {
		req.Header.Set("Urgency", msg.Urgency)
	}
	if msg.Topic != "" {
		req.Header.Set("Topic", msg.Topic)
	}

	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))

	res := Result{StatusCode: resp.StatusCode, Gone: resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := fmt.Sprintf("push service returned %d", resp.StatusCode)
		if t := bytes.TrimSpace(detail); len(t) > 0 {
			msg += ": " + string(t)
		}
		return res, errors.New(msg)
	}
	return res, nil
}

// TopicFor turns any identifier into a valid Topic header value.
func TopicFor(id string) string {
	sum := sha256.Sum256([]byte(id))
	return b64.EncodeToString(sum[:18]) // 24 characters
}

// encrypt builds an aes128gcm body with a single record (RFC 8291 section 3.4).
func encrypt(plaintext []byte, asPrivate *ecdh.PrivateKey, uaPublicRaw, authSecret, salt []byte) ([]byte, error) {
	uaPublic, err := ecdh.P256().NewPublicKey(uaPublicRaw)
	if err != nil {
		return nil, fmt.Errorf("p256dh: %w", err)
	}
	ecdhSecret, err := asPrivate.ECDH(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("ecdh: %w", err)
	}
	asPublicRaw := asPrivate.PublicKey().Bytes()

	keyInfo := append([]byte("WebPush: info\x00"), uaPublicRaw...)
	keyInfo = append(keyInfo, asPublicRaw...)
	ikm, err := hkdf.Key(sha256.New, ecdhSecret, authSecret, string(keyInfo), 32)
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
	// 0x02 marks the last (and only) record; no extra padding.
	padded := append(append([]byte{}, plaintext...), 0x02)

	header := make([]byte, 0, 16+4+1+len(asPublicRaw))
	header = append(header, salt...)
	header = binary.BigEndian.AppendUint32(header, recordSize)
	header = append(header, byte(len(asPublicRaw)))
	header = append(header, asPublicRaw...)
	return gcm.Seal(header, nonce, padded, nil), nil
}

// vapidToken signs the ES256 JWT for one push service origin. Push services
// cap expiry at 24 hours; 12 hours leaves room for clock skew.
func (s *Sender) vapidToken(audience string) (string, error) {
	if s.Keys == nil || s.Keys.signer == nil {
		return "", errors.New("vapid keys not loaded")
	}
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	header := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(map[string]any{
		"aud": audience,
		"exp": now().Add(12 * time.Hour).Unix(),
		"sub": s.Subject,
	})
	if err != nil {
		return "", err
	}
	signingInput := header + "." + b64.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signingInput))
	der, err := ecdsa.SignASN1(rand.Reader, s.Keys.signer, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign vapid token: %w", err)
	}
	var sig struct{ R, S *big.Int }
	if _, err := asn1.Unmarshal(der, &sig); err != nil {
		return "", fmt.Errorf("sign vapid token: %w", err)
	}
	raw := make([]byte, 64)
	sig.R.FillBytes(raw[:32])
	sig.S.FillBytes(raw[32:])
	return signingInput + "." + b64.EncodeToString(raw), nil
}

// decodeB64 accepts base64url with or without padding, which is how browsers
// and other libraries hand these values around.
func decodeB64(s string) ([]byte, error) {
	return b64.DecodeString(strings.TrimRight(s, "="))
}
