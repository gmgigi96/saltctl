package main

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

// credEncode reproduces the web UI's do_encode(x, true) == ArcMD5(x):
// SHA512( hex(MD5(x)) ), where the MD5 is rendered as a 32-char lowercase
// hex string and that ASCII string is the SHA-512 input. Both the username
// and password are sent to /login.cgi in this form.
func credEncode(s string) string {
	m := md5.Sum([]byte(s))
	mhex := hex.EncodeToString(m[:]) // 32 lowercase hex chars
	sh := sha512.Sum512([]byte(mhex))
	return hex.EncodeToString(sh[:])
}

var dataImgRe = regexp.MustCompile(`src="(data:image/gif;base64,[^"]+)"`)

// pageSecrets holds the per-page-load values the firmware embeds in a spacer
// <img> as a data: URI. ArcBase._t() decodes base64(uri[78:]) and slices:
//
//	[0:32]  -> AES-256 key (ASCII)
//	[32:48] -> AES-CBC IV (ASCII)
//	[48:]   -> httoken (anti-CSRF nonce)
type pageSecrets struct {
	token string
	key   []byte
	iv    []byte
}

// scrapeSecrets extracts the token/key/iv from a freshly loaded page's HTML.
// Mirrors ArcBase._t: it uses the first data: image on the page.
func scrapeSecrets(html string) (pageSecrets, error) {
	m := dataImgRe.FindStringSubmatch(html)
	if m == nil {
		return pageSecrets{}, fmt.Errorf("no token image found on page")
	}
	uri := m[1]
	if len(uri) <= 78 {
		return pageSecrets{}, fmt.Errorf("token image too short")
	}
	payload, err := base64.StdEncoding.DecodeString(uri[78:])
	if err != nil {
		return pageSecrets{}, fmt.Errorf("decode token image: %w", err)
	}
	if len(payload) < 48 {
		return pageSecrets{}, fmt.Errorf("token payload too short (%d bytes)", len(payload))
	}
	return pageSecrets{
		key:   payload[0:32],
		iv:    payload[32:48],
		token: string(payload[48:]),
	}, nil
}

// aesEncode reproduces do_encode(s) for a non-boolean value: AES-256-CBC with
// zero padding to a 16-byte multiple, rendered as "%xx%xx..." (byteArrayToHTML).
// Used for the `action` and each changed field of an /apply_abstract.cgi POST.
func aesEncode(sec pageSecrets, s string) (string, error) {
	block, err := aes.NewCipher(sec.key)
	if err != nil {
		return "", fmt.Errorf("aes key (%d bytes): %w", len(sec.key), err)
	}
	pt := []byte(s)
	// IntToByteArray pads with zero bytes up to ceil(len/16)*16.
	if r := len(pt) % 16; r != 0 {
		pt = append(pt, make([]byte, 16-r)...)
	}
	out := make([]byte, len(pt))
	cipher.NewCBCEncrypter(block, sec.iv).CryptBlocks(out, pt)
	var b strings.Builder
	for _, by := range out {
		fmt.Fprintf(&b, "%%%02x", by)
	}
	return b.String(), nil
}
