package webpush

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The worked example from RFC 8291 §5.
//
// This vector is the whole reason to write the encryption rather than take a dependency
// on somebody else's: it is the difference between "our code agrees with itself" and "our
// code agrees with what a browser will try to decrypt". A wrong info string round-trips
// perfectly and fails only against a real push service, as a 400 that looks like a
// malformed request.
const (
	vectorSalt      = "DGv6ra1nlYgDCS1FRnbzlw"
	vectorASPrivate = "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"
	vectorUAPublic  = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	vectorUAPrivate = "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"
	vectorAuth      = "BTBZMqHH6r4Tts7J_aSIgg"
	vectorPlaintext = "When I grow up, I want to be a watermelon"
	vectorBody      = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27ml" +
		"mlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPT" +
		"pK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
)

func decode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := b64.DecodeString(s)
	if err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return b
}

func TestSealMatchesRFC8291(t *testing.T) {
	ephemeral, err := ecdh.P256().NewPrivateKey(decode(t, vectorASPrivate))
	if err != nil {
		t.Fatalf("load application server key: %v", err)
	}
	got, err := seal(decode(t, vectorUAPublic), decode(t, vectorAuth), []byte(vectorPlaintext),
		ephemeral, decode(t, vectorSalt))
	if err != nil {
		t.Fatalf("seal(): %v", err)
	}
	if want := decode(t, vectorBody); !bytes.Equal(got, want) {
		t.Errorf("seal() does not match RFC 8291 §5\n got %x\nwant %x", got, want)
	}
}

func TestSealFramesTheHeader(t *testing.T) {
	ephemeral, _ := ecdh.P256().NewPrivateKey(decode(t, vectorASPrivate))
	body, err := seal(decode(t, vectorUAPublic), decode(t, vectorAuth), []byte(vectorPlaintext),
		ephemeral, decode(t, vectorSalt))
	if err != nil {
		t.Fatalf("seal(): %v", err)
	}
	if !bytes.Equal(body[:16], decode(t, vectorSalt)) {
		t.Error("body does not begin with the salt")
	}
	if got := binary.BigEndian.Uint32(body[16:20]); got != recordSize {
		t.Errorf("record size = %d, want %d", got, recordSize)
	}
	if body[20] != 65 {
		t.Errorf("key id length = %d, want 65", body[20])
	}
	if !bytes.Equal(body[21:86], ephemeral.PublicKey().Bytes()) {
		t.Error("key id is not the application server's ephemeral public key")
	}
}

func TestSealRoundTrips(t *testing.T) {
	// A fresh ephemeral key each time, so this covers the path Send actually takes rather
	// than only the fixed vector.
	ephemeral, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var salt [16]byte
	copy(salt[:], decode(t, vectorSalt))

	body, err := seal(decode(t, vectorUAPublic), decode(t, vectorAuth), []byte(vectorPlaintext), ephemeral, salt[:])
	if err != nil {
		t.Fatalf("seal(): %v", err)
	}
	got, err := open(t, body, decode(t, vectorUAPrivate), decode(t, vectorUAPublic), decode(t, vectorAuth))
	if err != nil {
		t.Fatalf("open(): %v", err)
	}
	if string(got) != vectorPlaintext {
		t.Errorf("round trip = %q, want %q", got, vectorPlaintext)
	}
}

func TestAPayloadTooLargeNeverLeavesTheProcess(t *testing.T) {
	var reached bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))
	defer srv.Close()

	// 413 is not retryable: the same bytes would be sent again and refused again. Catching
	// it here means the error can say how far over the limit it was, and means a push
	// service is not asked a question whose answer is already known.
	err := testSender(t).Send(t.Context(), Subscription{
		Endpoint: srv.URL, P256dh: vectorUAPublic, Auth: vectorAuth,
	}, bytes.Repeat([]byte("x"), MaxBody+1))

	var f *Failure
	if !errors.As(err, &f) || f.Reason != ReasonTooLarge {
		t.Fatalf("Send() with an oversized payload = %v, want a %s failure", err, ReasonTooLarge)
	}
	if reached {
		t.Error("the request was sent anyway")
	}
}

func TestAuthorizationIsTheVAPIDSingleHeaderForm(t *testing.T) {
	s := testSender(t)
	got, err := s.authorization("https://push.example.com/subscription/abc?token=xyz")
	if err != nil {
		t.Fatalf("authorization(): %v", err)
	}
	if !strings.HasPrefix(got, "vapid t=") || !strings.Contains(got, ",k=") {
		t.Fatalf("authorization() = %q, want the RFC 8292 single-header form", got)
	}

	token := strings.TrimPrefix(strings.Split(got, ",k=")[0], "vapid t=")
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments, want 3", len(parts))
	}
	// JWS wants r‖s, each 32 bytes. ecdsa.SignASN1 is the obvious call and produces DER,
	// which every push service rejects as a 400 that looks like a malformed request.
	if sig := decode(t, parts[2]); len(sig) != 64 {
		t.Errorf("signature is %d bytes, want 64 — this looks like ASN.1", len(sig))
	}

	var claims struct {
		Aud string `json:"aud"`
		Sub string `json:"sub"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(decode(t, parts[1]), &claims); err != nil {
		t.Fatalf("claims: %v", err)
	}
	// The origin, not the full endpoint: a token scoped to one subscription would be a
	// bearer token for it rather than an identification of the sender.
	if claims.Aud != "https://push.example.com" {
		t.Errorf("aud = %q, want the push service origin alone", claims.Aud)
	}
	if claims.Sub != "https://btw.example.com" {
		t.Errorf("sub = %q, want the instance address", claims.Sub)
	}
	if exp := time.Unix(claims.Exp, 0); !exp.After(time.Now()) || exp.After(time.Now().Add(24*time.Hour)) {
		t.Errorf("exp = %s, want inside the next 24 hours", exp)
	}
}

func TestAGoneSubscriptionIsDeletedRatherThanRetried(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusGone} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "unsubscribed", status)
		}))
		err := testSender(t).Send(t.Context(), Subscription{
			Endpoint: srv.URL, P256dh: vectorUAPublic, Auth: vectorAuth,
		}, []byte("btw"))
		srv.Close()

		if !Gone(err) {
			t.Errorf("Send() against %d = %v, want a gone failure", status, err)
		}
	}
}

func TestABusyPushServiceIsNotGone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	err := testSender(t).Send(t.Context(), Subscription{
		Endpoint: srv.URL, P256dh: vectorUAPublic, Auth: vectorAuth,
	}, []byte("btw"))

	var f *Failure
	if !errors.As(err, &f) || f.Reason != ReasonBusy {
		t.Fatalf("Send() against 429 = %v, want a %s failure", err, ReasonBusy)
	}
	if Gone(err) {
		t.Error("a busy push service was treated as gone; the device would have been deleted")
	}
}

func TestSendCarriesTheHeadersThatMakeANudgeTimely(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	if err := testSender(t).Send(t.Context(), Subscription{
		Endpoint: srv.URL, P256dh: vectorUAPublic, Auth: vectorAuth,
	}, []byte("go to the circus")); err != nil {
		t.Fatalf("Send(): %v", err)
	}

	// TTL is what stops a nudge arriving at midnight about an afternoon that has passed,
	// and Topic is what stops three arriving together when a flat battery comes back.
	if got.Get("TTL") != "3600" {
		t.Errorf("TTL = %q, want 3600", got.Get("TTL"))
	}
	if got.Get("Topic") != Topic {
		t.Errorf("Topic = %q, want %q", got.Get("Topic"), Topic)
	}
	if got.Get("Content-Encoding") != "aes128gcm" {
		t.Errorf("Content-Encoding = %q, want aes128gcm", got.Get("Content-Encoding"))
	}
}

func testSender(t *testing.T) *Sender {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate vapid key: %v", err)
	}
	pub, err := key.PublicKey.ECDH()
	if err != nil {
		t.Fatalf("encode vapid key: %v", err)
	}
	return NewSender(key, pub.Bytes(), "https://btw.example.com")
}

// open is the browser's half, for tests only: parse the aes128gcm header, run the key
// agreement from the user agent's side, and decrypt.
func open(t *testing.T, body, uaPrivateRaw, uaPublicRaw, authSecret []byte) ([]byte, error) {
	t.Helper()
	idLen := int(body[20])
	asPublicRaw := body[21 : 21+idLen]
	salt := body[:16]
	ciphertext := body[21+idLen:]

	uaPrivate, err := ecdh.P256().NewPrivateKey(uaPrivateRaw)
	if err != nil {
		return nil, err
	}
	asPublic, err := ecdh.P256().NewPublicKey(asPublicRaw)
	if err != nil {
		return nil, err
	}
	shared, err := uaPrivate.ECDH(asPublic)
	if err != nil {
		return nil, err
	}

	keyInfo := append([]byte("WebPush: info\x00"), uaPublicRaw...)
	keyInfo = append(keyInfo, asPublicRaw...)
	ikm, err := hkdf.Key(sha256.New, shared, authSecret, string(keyInfo), 32)
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
	record, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	return bytes.TrimRight(record, "\x02"), nil
}
