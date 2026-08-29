package store

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
)

// VAPIDKeys returns the instance's application server keypair, generating it on first
// call.
//
// One keypair per instance, stored in main.db, and never rotated. Losing it invalidates
// every subscription at once and no amount of re-registering repairs them, because those
// endpoints were bound to the old key — which is why it lives in the file that gets backed
// up, and why rotation is not a feature.
//
// Stored as PKCS#8 and the uncompressed public point, so what comes back out is what the
// browser was given.
func (s *Store) VAPIDKeys(ctx context.Context) (*ecdsa.PrivateKey, []byte, error) {
	var der, pub []byte
	err := s.main.QueryRowContext(ctx, `SELECT private_key, public_key FROM vapid WHERE singleton = 1`).
		Scan(&der, &pub)
	switch {
	case err == nil:
		key, err := x509.ParsePKCS8PrivateKey(der)
		if err != nil {
			return nil, nil, fmt.Errorf("parse vapid key: %w", err)
		}
		ec, ok := key.(*ecdsa.PrivateKey)
		if !ok {
			return nil, nil, errors.New("stored vapid key is not an ecdsa key")
		}
		return ec, pub, nil
	case !errors.Is(err, sql.ErrNoRows):
		return nil, nil, fmt.Errorf("read vapid key: %w", err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate vapid key: %w", err)
	}
	der, err = x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal vapid key: %w", err)
	}
	// Via crypto/ecdh rather than the deprecated elliptic.Marshal: same 65 uncompressed
	// bytes, and the one encoding the browser's applicationServerKey expects.
	ecdhPub, err := key.PublicKey.ECDH()
	if err != nil {
		return nil, nil, fmt.Errorf("encode vapid public key: %w", err)
	}
	pub = ecdhPub.Bytes()

	// DO NOTHING rather than an error: two processes starting together should end with one
	// keypair, and the loser reads the winner's rather than failing to start.
	if _, err := s.main.ExecContext(ctx,
		`INSERT INTO vapid (singleton, private_key, public_key, created_at) VALUES (1, ?, ?, ?)
		 ON CONFLICT (singleton) DO NOTHING`, der, pub, unix(s.Now())); err != nil {
		return nil, nil, fmt.Errorf("store vapid key: %w", err)
	}
	return s.VAPIDKeys(ctx)
}
