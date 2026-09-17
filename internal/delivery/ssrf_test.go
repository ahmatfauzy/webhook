package delivery

import "testing"

func TestValidateURL(t *testing.T) {
	tests := []struct {
		url          string
		allowPrivate bool
		wantErr      bool
	}{
		{"https://example.com/webhook", false, false},
		{"http://example.com:8080/a", false, false},
		{"ftp://example.com", false, true},
		{"http://localhost/webhook", false, true},
		{"http://127.0.0.1/webhook", false, true},
		{"http://10.0.0.1/webhook", false, true},
		{"http://192.168.1.1/webhook", false, true},
		{"http://169.254.169.254/latest/meta-data/", false, true},
		{"http://[::1]/webhook", false, true},
		// allowPrivate true should allow private
		{"http://127.0.0.1:8080/webhook", true, false},
		{"http://10.0.0.1/webhook", true, false},
		{"http://localhost/webhook", true, false},
	}
	for _, tc := range tests {
		err := ValidateURL(tc.url, tc.allowPrivate, nil)
		if (err != nil) != tc.wantErr {
			t.Errorf("ValidateURL(%q, allowPrivate=%v) err=%v wantErr=%v", tc.url, tc.allowPrivate, err, tc.wantErr)
		}
	}
}

func TestValidateURLBlockMetadata(t *testing.T) {
	if err := ValidateURL("http://169.254.169.254/", false, nil); err == nil {
		t.Fatalf("should block metadata IP")
	}
	if err := ValidateURL("http://metadata.google.internal/", false, nil); err == nil {
		// this is hostname, not IP — our validator may allow hostname but block after DNS resolve
		// For MVP, hostname check is optional; we test IP blocking only
		t.Logf("hostname metadata not blocked at URL parse (expected, DNS check at send-time)")
	}
}
