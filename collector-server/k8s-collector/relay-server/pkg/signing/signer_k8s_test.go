package signing

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"

	"github.com/tidwall/gjson"
)

// Shared cross-repo signing vector — MUST stay byte-identical to the agent's
// runner/pkg/relaysig/verify_test.go vector so the two implementations can't
// silently drift.
const (
	vectorSeedB64 = "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA="
	vectorPubB64  = "ebVWLo/mVPlAeLES6KmLp5AfhTrmlb7X4OORC60ElmQ="
	vectorSigB64  = "EuCrun1jNPY3biXu7Faf7M3K1mAqMF18jXxm36ltMoHFjZfCaWafUzgorxWysf4mb/I6vgip2C+b++FMytCCDw=="
	vectorBody    = `{"account_id":"acct-1","action_name":"delete_workload","action_params":{"kind":"deployment","name":"web","namespace":"shop"},"timestamp":1700000000}`
)

func newVectorSigner(t *testing.T) *Signer {
	t.Helper()
	s, err := NewSigner(vectorSeedB64, "test-key", testLogger())
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	if s == nil {
		t.Fatal("nil signer")
	}
	return s
}

// TestSignK8sBody_VectorStability pins the cross-impl contract: signing the
// fixed body under the fixed seed yields exactly the fixed signature the agent
// expects.
func TestSignK8sBody_VectorStability(t *testing.T) {
	s := newVectorSigner(t)
	payload := []byte(`{"body":` + vectorBody + `,"request_id":"r1"}`)

	signed, err := s.SignK8sBody(payload)
	if err != nil {
		t.Fatalf("SignK8sBody: %v", err)
	}
	gotSig := gjson.GetBytes(signed, "relay_signature").String()
	if gotSig != vectorSigB64 {
		t.Fatalf("relay_signature drift:\n got %s\nwant %s", gotSig, vectorSigB64)
	}
}

// TestSignK8sBody_BodyBytesPreserved is the load-bearing assertion: sjson must
// insert the relay_* fields WITHOUT reformatting `body`, so the agent's
// gjson(body).Raw still verifies under the signature.
func TestSignK8sBody_BodyBytesPreserved(t *testing.T) {
	s := newVectorSigner(t)
	payload := []byte(`{"body":` + vectorBody + `,"request_id":"r1","no_sinks":true}`)

	signed, err := s.SignK8sBody(payload)
	if err != nil {
		t.Fatalf("SignK8sBody: %v", err)
	}
	pub, _ := base64.StdEncoding.DecodeString(vectorPubB64)
	sig, _ := base64.StdEncoding.DecodeString(gjson.GetBytes(signed, "relay_signature").String())
	bodyRaw := gjson.GetBytes(signed, "body").Raw
	if !ed25519.Verify(ed25519.PublicKey(pub), []byte(bodyRaw), sig) {
		t.Fatal("body bytes changed after sjson insertion — signature no longer verifies")
	}
}

// TestSignK8sBody_AdditiveOnly confirms the agent's HMAC/RSA fields are never
// written, so old agents ignore the new fields and reads don't regress.
func TestSignK8sBody_AdditiveOnly(t *testing.T) {
	s := newVectorSigner(t)
	payload := []byte(`{"body":` + vectorBody + `}`)

	signed, err := s.SignK8sBody(payload)
	if err != nil {
		t.Fatalf("SignK8sBody: %v", err)
	}
	for _, forbidden := range []string{"signature", "partial_auth_a", "partial_auth_b", "signed_payload"} {
		if gjson.GetBytes(signed, forbidden).Exists() {
			t.Errorf("SignK8sBody wrote forbidden field %q", forbidden)
		}
	}
	for _, want := range []string{"relay_signature", "relay_signed_at", "relay_nonce", "relay_key_id"} {
		if !gjson.GetBytes(signed, want).Exists() {
			t.Errorf("SignK8sBody missing field %q", want)
		}
	}
}

func TestSignK8sBody_NoBody(t *testing.T) {
	s := newVectorSigner(t)
	if _, err := s.SignK8sBody([]byte(`{"request_id":"r1"}`)); err == nil {
		t.Fatal("expected error when payload has no body")
	}
}
