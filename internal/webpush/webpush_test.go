package webpush

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/webpush/webpushtest"
)

// RFC 8291 Appendix A.
const (
	rfcPlaintext = "When I grow up, I want to be a watermelon"
	rfcASPrivate = "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"
	rfcASPublic  = "BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8"
	rfcUAPrivate = "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"
	rfcUAPublic  = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	rfcSalt      = "DGv6ra1nlYgDCS1FRnbzlw"
	rfcAuth      = "BTBZMqHH6r4Tts7J_aSIgg"
	rfcMessage   = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
)

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := decodeB64(s)
	if err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return b
}

func TestEncryptMatchesRFC8291Vector(t *testing.T) {
	asPrivate, err := ecdh.P256().NewPrivateKey(mustB64(t, rfcASPrivate))
	if err != nil {
		t.Fatal(err)
	}
	if got := b64.EncodeToString(asPrivate.PublicKey().Bytes()); got != rfcASPublic {
		t.Fatalf("as_public = %s, want %s", got, rfcASPublic)
	}
	body, err := encrypt([]byte(rfcPlaintext), asPrivate, mustB64(t, rfcUAPublic), mustB64(t, rfcAuth), mustB64(t, rfcSalt))
	if err != nil {
		t.Fatal(err)
	}
	if got := b64.EncodeToString(body); got != rfcMessage {
		t.Fatalf("message mismatch\n got %s\nwant %s", got, rfcMessage)
	}

	uaPrivate, err := ecdh.P256().NewPrivateKey(mustB64(t, rfcUAPrivate))
	if err != nil {
		t.Fatal(err)
	}
	sub := &webpushtest.Subscriber{Private: uaPrivate, AuthSecret: mustB64(t, rfcAuth)}
	plain, err := sub.Decrypt(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != rfcPlaintext {
		t.Fatalf("decrypted %q", plain)
	}
}

func TestKeysRoundTrip(t *testing.T) {
	k, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	if len(mustB64(t, k.Public)) != 65 || len(mustB64(t, k.Private)) != 32 {
		t.Fatalf("unexpected key sizes: %q %q", k.Public, k.Private)
	}
	again, err := ParseKeys(k.Private)
	if err != nil {
		t.Fatal(err)
	}
	if again.Public != k.Public {
		t.Fatal("public key not derived consistently")
	}
	if _, err := ParseKeys("not-a-key"); err == nil {
		t.Fatal("expected error for invalid key")
	}
	// Padded input is accepted.
	if _, err := ParseKeys(k.Private + "="); err != nil {
		t.Fatalf("padded key: %v", err)
	}
}

func TestValidateSubscription(t *testing.T) {
	s := webpushtest.NewSubscriber()
	ok := Subscription{Endpoint: "https://push.example.com/abc", P256dh: s.P256dh(), Auth: s.Auth()}
	if err := ValidateSubscription(ok); err != nil {
		t.Fatalf("valid subscription rejected: %v", err)
	}
	cases := map[string]Subscription{
		"http endpoint":     {Endpoint: "http://push.example.com/abc", P256dh: s.P256dh(), Auth: s.Auth()},
		"userinfo":          {Endpoint: "https://user:pw@push.example.com/abc", P256dh: s.P256dh(), Auth: s.Auth()},
		"no host":           {Endpoint: "https:///abc", P256dh: s.P256dh(), Auth: s.Auth()},
		"long endpoint":     {Endpoint: "https://push.example.com/" + strings.Repeat("a", 2048), P256dh: s.P256dh(), Auth: s.Auth()},
		"bad p256dh":        {Endpoint: ok.Endpoint, P256dh: "AAAA", Auth: s.Auth()},
		"p256dh not base64": {Endpoint: ok.Endpoint, P256dh: "!!!", Auth: s.Auth()},
		"short auth":        {Endpoint: ok.Endpoint, P256dh: s.P256dh(), Auth: "AAAA"},
	}
	for name, sub := range cases {
		if err := ValidateSubscription(sub); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestSendPostsEncryptedSignedMessage(t *testing.T) {
	keys, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	sub := webpushtest.NewSubscriber()

	var got *http.Request
	var body []byte
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	fixed := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	sender := &Sender{Keys: keys, Subject: "mailto:ops@example.com", Client: srv.Client(), now: func() time.Time { return fixed }}
	res, err := sender.Send(context.Background(),
		Subscription{Endpoint: srv.URL + "/push/xyz", P256dh: sub.P256dh(), Auth: sub.Auth()},
		Message{Payload: []byte(`{"title":"hi"}`), TTL: time.Hour, Urgency: "high", Topic: TopicFor("rule-1")})
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated || res.Gone {
		t.Fatalf("result %+v", res)
	}

	if got.Header.Get("Content-Encoding") != "aes128gcm" || got.Header.Get("TTL") != "3600" ||
		got.Header.Get("Urgency") != "high" || got.Header.Get("Topic") != TopicFor("rule-1") {
		t.Fatalf("headers %v", got.Header)
	}
	plain, err := sub.Decrypt(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != `{"title":"hi"}` {
		t.Fatalf("payload %q", plain)
	}

	token, key, err := webpushtest.ParseAuthorization(got.Header.Get("Authorization"))
	if err != nil {
		t.Fatal(err)
	}
	if key != keys.Public {
		t.Fatal("k is not the VAPID public key")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token %q", token)
	}
	var claims struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(mustB64(t, parts[1]), &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Aud != srv.URL || claims.Sub != "mailto:ops@example.com" || claims.Exp != fixed.Add(12*time.Hour).Unix() {
		t.Fatalf("claims %+v", claims)
	}

	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), mustB64(t, keys.Public))
	if err != nil {
		t.Fatal(err)
	}
	sig := mustB64(t, parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(sig) != 64 {
		t.Fatalf("signature is %d bytes", len(sig))
	}
	// JWT ES256 signatures are raw r||s; VerifyASN1 wants DER.
	der, err := asn1.Marshal(struct{ R, S *big.Int }{new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])})
	if err != nil {
		t.Fatal(err)
	}
	if !ecdsa.VerifyASN1(pub, digest[:], der) {
		t.Fatal("JWT signature does not verify")
	}
}

func TestSendReportsGoneAndErrors(t *testing.T) {
	keys, _ := GenerateKeys()
	sub := webpushtest.NewSubscriber()
	for _, tc := range []struct {
		status int
		gone   bool
	}{{http.StatusGone, true}, {http.StatusNotFound, true}, {http.StatusTooManyRequests, false}, {http.StatusBadRequest, false}} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			w.Write([]byte("nope"))
		}))
		sender := &Sender{Keys: keys, Subject: "mailto:a@b.c", Client: srv.Client()}
		res, err := sender.Send(context.Background(), Subscription{Endpoint: srv.URL, P256dh: sub.P256dh(), Auth: sub.Auth()}, Message{Payload: []byte("x")})
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), "nope") {
			t.Errorf("%d: err = %v", tc.status, err)
		}
		if res.Gone != tc.gone || res.StatusCode != tc.status {
			t.Errorf("%d: result %+v", tc.status, res)
		}
	}
}

func TestSendRejectsOversizedPayload(t *testing.T) {
	keys, _ := GenerateKeys()
	sub := webpushtest.NewSubscriber()
	sender := &Sender{Keys: keys, Subject: "mailto:a@b.c"}
	_, err := sender.Send(context.Background(), Subscription{Endpoint: "https://push.example.com", P256dh: sub.P256dh(), Auth: sub.Auth()},
		Message{Payload: bytes.Repeat([]byte("a"), MaxPayload+1)})
	if err == nil {
		t.Fatal("expected size error")
	}
}

func TestMaxPayloadFitsOneRecord(t *testing.T) {
	keys, _ := GenerateKeys()
	sub := webpushtest.NewSubscriber()
	var size int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		size = len(b)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	sender := &Sender{Keys: keys, Subject: "mailto:a@b.c", Client: srv.Client()}
	if _, err := sender.Send(context.Background(), Subscription{Endpoint: srv.URL, P256dh: sub.P256dh(), Auth: sub.Auth()},
		Message{Payload: bytes.Repeat([]byte("a"), MaxPayload)}); err != nil {
		t.Fatal(err)
	}
	if size != recordSize {
		t.Fatalf("body is %d bytes, want %d", size, recordSize)
	}
}

func TestTopicFor(t *testing.T) {
	topic := TopicFor("3f1c2a52-8a7e-4c7b-9a51-2b1f9d0e7c11")
	if len(topic) > 32 || strings.ContainsAny(topic, "+/=") {
		t.Fatalf("invalid topic %q", topic)
	}
	if TopicFor("a") == TopicFor("b") {
		t.Fatal("topics collide")
	}
}
