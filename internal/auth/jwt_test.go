package auth

import (
	"testing"
	"time"
)

func TestSignAndVerifyRS256(t *testing.T) {
	privPEM, pubPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	priv, err := ParsePrivateKeyPEM(privPEM)
	if err != nil {
		t.Fatalf("ParsePrivateKeyPEM: %v", err)
	}
	pub, err := ParsePublicKeyPEM(pubPEM)
	if err != nil {
		t.Fatalf("ParsePublicKeyPEM: %v", err)
	}
	now := time.Unix(1000, 0)
	token, err := SignRS256(priv, Claims{TenantID: "11111111-1111-1111-1111-111111111111", Subject: "dev", IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix()})
	if err != nil {
		t.Fatalf("SignRS256: %v", err)
	}
	claims, err := (Verifier{PublicKey: pub, Now: func() time.Time { return now }}).Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.TenantID != "11111111-1111-1111-1111-111111111111" || claims.Subject != "dev" {
		t.Fatalf("claims=%+v", claims)
	}
}

func TestVerifyExpiredToken(t *testing.T) {
	privPEM, pubPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	priv, err := ParsePrivateKeyPEM(privPEM)
	if err != nil {
		t.Fatalf("ParsePrivateKeyPEM: %v", err)
	}
	pub, err := ParsePublicKeyPEM(pubPEM)
	if err != nil {
		t.Fatalf("ParsePublicKeyPEM: %v", err)
	}
	token, err := SignRS256(priv, Claims{TenantID: "11111111-1111-1111-1111-111111111111", Subject: "dev", IssuedAt: 1, ExpiresAt: 2})
	if err != nil {
		t.Fatalf("SignRS256: %v", err)
	}
	_, err = (Verifier{PublicKey: pub, Now: func() time.Time { return time.Unix(3, 0) }}).Verify(token)
	if err != ErrExpiredToken {
		t.Fatalf("err=%v want %v", err, ErrExpiredToken)
	}
}

func TestVerifyRejectsMissingOrZeroExp(t *testing.T) {
	privPEM, pubPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	priv, err := ParsePrivateKeyPEM(privPEM)
	if err != nil {
		t.Fatalf("ParsePrivateKeyPEM: %v", err)
	}
	pub, err := ParsePublicKeyPEM(pubPEM)
	if err != nil {
		t.Fatalf("ParsePublicKeyPEM: %v", err)
	}
	token, err := SignRS256(priv, Claims{TenantID: "11111111-1111-1111-1111-111111111111", Subject: "dev", IssuedAt: time.Now().Unix(), ExpiresAt: 0})
	if err != nil {
		t.Fatalf("SignRS256: %v", err)
	}
	if _, err := (Verifier{PublicKey: pub}).Verify(token); err != ErrExpiredToken {
		t.Fatalf("err=%v want %v", err, ErrExpiredToken)
	}
}
