package update

import (
	"strings"
	"testing"
)

func TestVerifyChecksumsSignatureAcceptsCRLFArmoredSignature(t *testing.T) {
	checksumsPayload := []byte("abc123  tunnel_v0.1.9_linux_amd64.tar.gz\n")
	publicKeyBase64, _, signaturePayload := mustReleaseSignature(t, checksumsPayload)
	crlfSignaturePayload := []byte(strings.ReplaceAll(string(signaturePayload), "\n", "\r\n"))

	if err := verifyChecksumsSignatureWithPublicKeyBase64(publicKeyBase64)(checksumsPayload, crlfSignaturePayload); err != nil {
		t.Fatalf("verifyChecksumsSignatureWithPublicKeyBase64 returned error for CRLF signature: %v", err)
	}
}
