// Package arcadyan implements the Arcadyan "WebBase" admin-UI protocol used by
// Salt's fibre boxes: login, the per-page-load token, and the AES request
// signing. It is model-agnostic; specific boxes build on it.
package arcadyan

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// CredEncode reproduces the UI's do_encode(x, true) == ArcMD5(x):
// SHA512( hex(MD5(x)) ). Username and password are posted to login.cgi so.
func CredEncode(s string) string {
	m := md5.Sum([]byte(s))
	mhex := hex.EncodeToString(m[:])
	sh := sha512.Sum512([]byte(mhex))
	return hex.EncodeToString(sh[:])
}

var dataImgRe = regexp.MustCompile(`src="(data:image/gif;base64,[^"]+)"`)

// Secrets are the per-page-load values embedded in a spacer <img> data URI.
// ArcBase._t decodes base64(uri[78:]) into key[0:32] ‖ iv[32:48] ‖ token[48:].
type Secrets struct {
	Token string
	Key   []byte
	IV    []byte
}

// ScrapeSecrets extracts the token/key/iv from a freshly loaded page.
func ScrapeSecrets(html string) (Secrets, error) {
	m := dataImgRe.FindStringSubmatch(html)
	if m == nil {
		return Secrets{}, fmt.Errorf("no token image found on page")
	}
	uri := m[1]
	if len(uri) <= 78 {
		return Secrets{}, fmt.Errorf("token image too short")
	}
	payload, err := base64.StdEncoding.DecodeString(uri[78:])
	if err != nil {
		return Secrets{}, fmt.Errorf("decode token image: %w", err)
	}
	if len(payload) < 48 {
		return Secrets{}, fmt.Errorf("token payload too short (%d bytes)", len(payload))
	}
	return Secrets{Key: payload[0:32], IV: payload[32:48], Token: string(payload[48:])}, nil
}

// aesEncode reproduces do_encode(s) for a value: AES-256-CBC, zero-padded to a
// 16-byte multiple, rendered "%xx%xx..." (byteArrayToHTML). Used for the
// `action` and each changed field of an apply_abstract.cgi POST.
func aesEncode(sec Secrets, s string) (string, error) {
	block, err := aes.NewCipher(sec.Key)
	if err != nil {
		return "", fmt.Errorf("aes key (%d bytes): %w", len(sec.Key), err)
	}
	pt := []byte(s)
	if r := len(pt) % 16; r != 0 {
		pt = append(pt, make([]byte, 16-r)...)
	}
	out := make([]byte, len(pt))
	cipher.NewCBCEncrypter(block, sec.IV).CryptBlocks(out, pt)
	var b strings.Builder
	for _, by := range out {
		fmt.Fprintf(&b, "%%%02x", by)
	}
	return b.String(), nil
}
