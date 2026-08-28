package httpadapter

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySignature(t *testing.T) {
	secret := []byte("unit-test-secret")
	body := []byte(`{"webhookEvent":"jira:issue_created"}`)

	cases := []struct {
		name   string
		header string
		body   []byte
		ok     bool
	}{
		{"valid signature", signBody("unit-test-secret", body), body, true},
		{"missing header", "", body, false},
		{"wrong scheme prefix", "sha1=" + hex.EncodeToString([]byte("x")), body, false},
		{"non-hex digest", "sha256=zznothex", body, false},
		{"tampered body", signBody("unit-test-secret", body), append(body, 'x'), false},
		{"wrong secret", signBody("other-secret", body), body, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := verifySignature(tc.body, tc.header, secret)
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.NotContains(t, err.Error(), "hmac") // errors carry no secret material
			}
		})
	}
}
