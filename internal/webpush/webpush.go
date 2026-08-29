// Package webpush delivers one encrypted message to one push subscription.
//
// Three RFCs and no dependency: RFC 8030 for the protocol, RFC 8291 for the message
// encryption, RFC 8292 for the application server identification. Everything they need is
// in the standard library — crypto/ecdh for the key agreement, crypto/hkdf for the
// derivation, crypto/aes for the record, crypto/ecdsa for the signature.
//
// The obvious alternative was SherClockHolmes/webpush-go, which is mostly these two
// hundred lines plus a JWT library. The argument against taking it is not size: this is
// the one part of btw with no fallback behaviour, so a message that fails to encrypt is
// the product not working, and a bug in it is a bug we would be reading somebody else's
// code to find. If this turns out to be three attempts and still wrong, the dependency is
// one import away and this paragraph is the record of why it was taken.
//
// The push service sees a length and a time, never a word — the body is encrypted to keys
// only the browser holds. What it does learn is *when* a person is nudged, and that is not
// encryptable.
package webpush

import (
	"bytes"
	"context"
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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Message headers that are the same on every send.
const (
	// TTL is the header that matters most here, and the one easiest to set to a day out of
	// habit. A phone that has been off for two hours should not receive "btw, ring the
	// dentist" at midnight; that nudge belonged to an afternoon that has passed. An hour,
	// and then the push service drops it on our behalf.
	TTL = 3600

	// Topic makes a push service collapse undelivered messages that share it, so a phone
	// coming back from a flat battery gets the most recent nudge, once, rather than three
	// at the door. At most 32 characters from the URL-safe alphabet, per RFC 8030.
	Topic = "btw"

	// MaxBody is the ceiling a push service is required to accept: 4096 bytes of encrypted
	// payload. The 86-byte header and the 17 bytes of padding delimiter and GCM tag come
	// out of it, which still leaves far more than a reminder should be.
	MaxBody = 4096
)

// recordSize is what the header advertises. One record, so it only has to be large enough
// for the whole payload.
const recordSize = MaxBody

var b64 = base64.RawURLEncoding

// Subscription is what a browser handed the page, and what the devices table stores.
type Subscription struct {
	Endpoint string
	P256dh   string // the UA's public key, base64url, 65 uncompressed bytes
	Auth     string // the shared auth secret, base64url, 16 bytes
}

// Sender holds what every send has in common: the application server keypair, the contact
// URI that identifies this instance, and one HTTP client.
type Sender struct {
	key     *ecdsa.PrivateKey
	pub     []byte
	subject string
	client  *http.Client
}

// NewSender builds one. `subject` is the VAPID contact URI — RFC 8292 allows an https URI
// as well as a mailto:, so btw uses its own public address rather than an operator address
// that will be stale within a year.
func NewSender(key *ecdsa.PrivateKey, pub []byte, subject string) *Sender {
	return &Sender{
		key:     key,
		pub:     pub,
		subject: subject,
		// A push service that hangs must not hold a scheduler tick. Ten seconds is
		// generous for a request whose body is under four kilobytes.
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// SetClient replaces the HTTP client. For tests; the daemon never calls it.
func (s *Sender) SetClient(c *http.Client) { s.client = c }

// PublicKey is what the browser passes as applicationServerKey when it subscribes.
func (s *Sender) PublicKey() string { return b64.EncodeToString(s.pub) }

// Send encrypts a payload to one subscription and posts it.
//
// The returned error is a *Failure when the push service answered, so a caller can tell a
// subscription that is gone from one that is merely busy.
func (s *Sender) Send(ctx context.Context, sub Subscription, payload []byte) error {
	body, err := s.seal(sub, payload)
	if err != nil {
		return err
	}
	if len(body) > MaxBody {
		// Refused here rather than sent, because a 413 is not retryable: the same bytes
		// would be sent again and refused again.
		return &Failure{Reason: ReasonTooLarge, Detail: fmt.Sprintf("%d bytes, limit %d", len(body), MaxBody)}
	}

	auth, err := s.authorization(sub.Endpoint)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build push request: %w", err)
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Length", strconv.Itoa(len(body)))
	req.Header.Set("TTL", strconv.Itoa(TTL))
	req.Header.Set("Topic", Topic)
	req.Header.Set("Urgency", "normal")

	resp, err := s.client.Do(req)
	if err != nil {
		return &Failure{Reason: ReasonUnreachable, Detail: err.Error()}
	}
	defer resp.Body.Close()
	// Bounded: a push service's error body is a sentence, and one that answers with a
	// megabyte of HTML should not be able to spend our memory on it.
	detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	return classify(resp, string(bytes.TrimSpace(detail)))
}

// seal encrypts a payload to a subscription, per RFC 8291.
func (s *Sender) seal(sub Subscription, payload []byte) ([]byte, error) {
	uaPublicRaw, err := b64.DecodeString(strings.TrimRight(sub.P256dh, "="))
	if err != nil {
		return nil, fmt.Errorf("decode subscription key: %w", err)
	}
	authSecret, err := b64.DecodeString(strings.TrimRight(sub.Auth, "="))
	if err != nil {
		return nil, fmt.Errorf("decode subscription auth: %w", err)
	}
	ephemeral, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ephemeral key: %w", err)
	}
	var salt [16]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return nil, fmt.Errorf("read salt: %w", err)
	}
	return seal(uaPublicRaw, authSecret, payload, ephemeral, salt[:])
}

// seal is the encryption proper, with the ephemeral key and salt passed in so a test can
// drive it against a known vector.
func seal(uaPublicRaw, authSecret, payload []byte, ephemeral *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	uaPublic, err := ecdh.P256().NewPublicKey(uaPublicRaw)
	if err != nil {
		return nil, fmt.Errorf("subscription key is not a P-256 point: %w", err)
	}
	shared, err := ephemeral.ECDH(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("key agreement: %w", err)
	}
	asPublic := ephemeral.PublicKey().Bytes()

	// RFC 8291 §3.4. The two public keys are bound into the derivation, so a message
	// encrypted for one subscription cannot be replayed at another even if the shared
	// secret somehow repeated.
	keyInfo := make([]byte, 0, len("WebPush: info")+1+len(uaPublicRaw)+len(asPublic))
	keyInfo = append(keyInfo, "WebPush: info\x00"...)
	keyInfo = append(keyInfo, uaPublicRaw...)
	keyInfo = append(keyInfo, asPublic...)

	ikm, err := hkdf.Key(sha256.New, shared, authSecret, string(keyInfo), 32)
	if err != nil {
		return nil, fmt.Errorf("derive ikm: %w", err)
	}
	// RFC 8188 §2.2, the aes128gcm content encoding.
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, fmt.Errorf("derive key: %w", err)
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, fmt.Errorf("derive nonce: %w", err)
	}

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	// 0x02 is RFC 8188's delimiter for the last record. One record, so the last is also
	// the first; 0x01 here would be read as "more records follow" and rejected.
	record := append(append([]byte{}, payload...), 0x02)
	ciphertext := gcm.Seal(nil, nonce, record, nil)

	// The aes128gcm header: salt(16) ‖ record size(4) ‖ key id length(1) ‖ key id.
	// The key id is the application server's ephemeral public key, which is how the
	// browser knows what to run the key agreement against.
	out := make([]byte, 0, 16+4+1+len(asPublic)+len(ciphertext))
	out = append(out, salt...)
	out = binary.BigEndian.AppendUint32(out, recordSize)
	out = append(out, byte(len(asPublic)))
	out = append(out, asPublic...)
	out = append(out, ciphertext...)
	return out, nil
}

// authorization builds the RFC 8292 single-header form: vapid t=<jwt>,k=<public key>.
func (s *Sender) authorization(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse endpoint: %w", err)
	}
	// The audience is the push service's origin and nothing else. A JWT scoped to the full
	// endpoint would be a bearer token for that one subscription; scoped to the origin it
	// is what it claims to be, an identification of the sender.
	aud := u.Scheme + "://" + u.Host

	header := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(map[string]any{
		"aud": aud,
		"exp": time.Now().Add(12 * time.Hour).Unix(),
		"sub": s.subject,
	})
	if err != nil {
		return "", fmt.Errorf("build claims: %w", err)
	}
	signing := header + "." + b64.EncodeToString(claims)

	digest := sha256.Sum256([]byte(signing))
	r, sInt, err := ecdsa.Sign(rand.Reader, s.key, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign vapid token: %w", err)
	}
	// JWS wants r and s each left-padded to 32 bytes and concatenated. ecdsa.SignASN1
	// would be the obvious call and produces a DER structure that every push service
	// rejects — silently, as a 400 that looks like a malformed request.
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	sInt.FillBytes(sig[32:])

	return "vapid t=" + signing + "." + b64.EncodeToString(sig) + ",k=" + b64.EncodeToString(s.pub), nil
}

// Why a send did not land. The vocabulary a device's last_error is written in, and what
// an operator reads when a device goes quiet.
const (
	ReasonGone        = "gone"        // 404, 410 — the subscription no longer exists
	ReasonRefused     = "refused"     // 401, 403 — the push service rejected our identity
	ReasonBusy        = "busy"        // 429, 5xx — ask again later
	ReasonTooLarge    = "too-large"   // 413 — and sending the same bytes again will not help
	ReasonInvalid     = "invalid"     // 400 — usually a malformed VAPID signature
	ReasonUnreachable = "unreachable" // nothing answered: DNS, a refused connection, a timeout
)

// Failure is a push service's answer, classified.
type Failure struct {
	Reason string
	Status int
	Detail string
}

func (f *Failure) Error() string {
	if f.Status == 0 {
		return fmt.Sprintf("push %s: %s", f.Reason, f.Detail)
	}
	return fmt.Sprintf("push %s (%d): %s", f.Reason, f.Status, f.Detail)
}

// Gone reports whether the subscription should be deleted rather than retried.
//
// Deleting on 410 rather than counting failures is the important one: a browser that has
// been reinstalled leaves an endpoint that will refuse forever, and a device list full of
// dead rows is how somebody concludes the product is broken when one of their four entries
// is live.
func Gone(err error) bool {
	var f *Failure
	return errors.As(err, &f) && f.Reason == ReasonGone
}

func classify(resp *http.Response, detail string) error {
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusNotFound, resp.StatusCode == http.StatusGone:
		return &Failure{Reason: ReasonGone, Status: resp.StatusCode, Detail: detail}
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return &Failure{Reason: ReasonRefused, Status: resp.StatusCode, Detail: detail}
	case resp.StatusCode == http.StatusRequestEntityTooLarge:
		return &Failure{Reason: ReasonTooLarge, Status: resp.StatusCode, Detail: detail}
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode >= 500:
		return &Failure{Reason: ReasonBusy, Status: resp.StatusCode, Detail: detail}
	default:
		return &Failure{Reason: ReasonInvalid, Status: resp.StatusCode, Detail: detail}
	}
}
