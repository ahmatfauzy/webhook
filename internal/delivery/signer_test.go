package delivery

import "testing"

func TestSignAndVerify(t *testing.T) {
	secret := "mysecret"
	ts := "1720000000"
	body := `{"id":"evt_01J","type":"order.created"}`
	sig := Sign(secret, ts, body)
	if sig[:3] != "v1=" {
		t.Fatalf("signature should start with v1=, got %s", sig)
	}
	if !Verify(secret, ts, body, sig) {
		t.Fatalf("verify should succeed")
	}
	if Verify(secret, ts, body, "v1=bad") {
		t.Fatalf("verify should fail for bad sig")
	}
	if Verify("wrong", ts, body, sig) {
		t.Fatalf("verify should fail for wrong secret")
	}
	// timestamp mismatch
	if Verify(secret, "1720000001", body, sig) {
		t.Fatalf("verify should fail for wrong timestamp")
	}
	// rawBody must be byte-exact
	if Verify(secret, ts, body+" ", sig) {
		t.Fatalf("verify should fail for modified body")
	}
}

func TestSignDeterministic(t *testing.T) {
	s1 := Sign("s", "1", `{"a":1}`)
	s2 := Sign("s", "1", `{"a":1}`)
	if s1 != s2 {
		t.Fatalf("sign should be deterministic")
	}
}
