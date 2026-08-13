// Package mediastream fetches WhatsApp media in ranges instead of all at once,
// so a video can start playing while the rest of it is still arriving.
//
// The wire format makes this possible. Media is AES-256-CBC, and CBC decrypts
// any 16-byte-aligned range as long as you also have the block immediately
// before it, which serves as that range's IV. A message may additionally carry
// a "streaming sidecar": one truncated HMAC per 64 KiB chunk, so each chunk can
// be authenticated on arrival rather than only after the whole file lands.
//
// Nothing in this package talks to the daemon, the store, or whatsmeow: it is
// given a URL and the keys from the message payload, and it fills a sparse file.
package mediastream

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"

	"go.mau.fi/whatsmeow/util/hkdfutil"
)

// ChunkSize is the sidecar's chunk size, and therefore the granularity at which
// this package fetches, verifies and records media.
const ChunkSize = 64 * 1024

// aesBlockSize is both the CBC block size and the size of the overlap a chunk
// needs from the chunk before it.
const aesBlockSize = aes.BlockSize

// macLength is how much of each HMAC WhatsApp keeps, both for the whole-file
// MAC trailer and for each sidecar entry.
const macLength = 10

// Keys is the expanded media key: everything needed to decrypt and authenticate
// one message's media.
type Keys struct {
	IV        []byte
	CipherKey []byte
	MACKey    []byte
}

// DeriveKeys expands a message's 32-byte media key exactly as WhatsApp does:
// HKDF-SHA256 with an empty salt and a media-type-specific info string
// ("WhatsApp Video Keys", "WhatsApp Audio Keys", ...), then split.
func DeriveKeys(mediaKey []byte, appInfo string) (Keys, error) {
	if len(mediaKey) == 0 {
		return Keys{}, errors.New("mediastream: media key is empty")
	}
	if appInfo == "" {
		return Keys{}, errors.New("mediastream: media type is empty")
	}
	expanded := hkdfutil.SHA256(mediaKey, nil, []byte(appInfo), 112)
	return Keys{
		IV:        expanded[:16],
		CipherKey: expanded[16:48],
		MACKey:    expanded[48:80],
	}, nil
}

// CiphertextLen is the encrypted length of a plaintext of the given size. CBC
// with PKCS#7 always pads, so a plaintext that is already a multiple of the
// block size still grows by a full block.
func CiphertextLen(plaintextLen int64) int64 {
	return plaintextLen + int64(aesBlockSize) - (plaintextLen % int64(aesBlockSize))
}

// ChunkCount is how many chunks a plaintext of this size is split into.
func ChunkCount(plaintextLen int64) int {
	if plaintextLen <= 0 {
		return 0
	}
	return int((plaintextLen + ChunkSize - 1) / ChunkSize)
}

// ChunkRange is the plaintext byte range chunk index covers.
func ChunkRange(index int, plaintextLen int64) (start, end int64) {
	start = int64(index) * ChunkSize
	end = start + ChunkSize
	if end > plaintextLen {
		end = plaintextLen
	}
	return start, end
}

// CipherRange is the ciphertext byte range that has to be fetched to decrypt
// chunks first..last inclusive. The range starts one block early (except for
// chunk 0, which uses the derived IV) because CBC needs the preceding block,
// and the final chunk extends through the padding block.
func CipherRange(first, last int, plaintextLen int64) (start, end int64) {
	cipherLen := CiphertextLen(plaintextLen)
	start = int64(first) * ChunkSize
	if start > 0 {
		start -= int64(aesBlockSize)
	}
	end = int64(last+1) * ChunkSize
	if last >= ChunkCount(plaintextLen)-1 || end > cipherLen {
		end = cipherLen
	}
	return start, end
}

// Decryptor decrypts individual chunks of one message's media.
type Decryptor struct {
	keys         Keys
	plaintextLen int64
	block        cipher.Block
}

func NewDecryptor(keys Keys, plaintextLen int64) (*Decryptor, error) {
	if len(keys.CipherKey) != 32 {
		return nil, fmt.Errorf("mediastream: cipher key is %d bytes, want 32", len(keys.CipherKey))
	}
	if plaintextLen <= 0 {
		return nil, errors.New("mediastream: plaintext length is unknown")
	}
	block, err := aes.NewCipher(keys.CipherKey)
	if err != nil {
		return nil, fmt.Errorf("mediastream: %w", err)
	}
	return &Decryptor{keys: keys, plaintextLen: plaintextLen, block: block}, nil
}

// DecryptChunk turns the ciphertext fetched for one chunk into that chunk's
// plaintext. iv is the 16 bytes preceding the chunk (the derived IV for chunk
// 0). The final chunk's padding is dropped by truncating to the length the
// message declared, which is stricter than trusting the padding bytes.
func (d *Decryptor) DecryptChunk(index int, iv, ciphertext []byte) ([]byte, error) {
	if len(iv) != aesBlockSize {
		return nil, fmt.Errorf("mediastream: chunk %d iv is %d bytes, want %d", index, len(iv), aesBlockSize)
	}
	if len(ciphertext) == 0 || len(ciphertext)%aesBlockSize != 0 {
		return nil, fmt.Errorf("mediastream: chunk %d ciphertext is %d bytes, not a block multiple", index, len(ciphertext))
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(d.block, iv).CryptBlocks(plaintext, ciphertext)

	start, end := ChunkRange(index, d.plaintextLen)
	want := int(end - start)
	if len(plaintext) < want {
		return nil, fmt.Errorf("mediastream: chunk %d decrypted to %d bytes, want at least %d", index, len(plaintext), want)
	}
	return plaintext[:want], nil
}

// SidecarLayout says which bytes a sidecar entry authenticates. WhatsApp's
// sidecar is documented only by implementations, and they disagree on whether
// the 16-byte overlap sits at the head of the chunk (the chunk's IV) or at its
// tail. Rather than guess, Verifier tries both against the first chunk it sees
// and remembers the one that matches.
type SidecarLayout int

const (
	SidecarLayoutUnknown SidecarLayout = iota
	// SidecarLayoutLeadingIV authenticates iv || chunk ciphertext.
	SidecarLayoutLeadingIV
	// SidecarLayoutTrailingBlock authenticates chunk ciphertext || the first
	// block of the next chunk.
	SidecarLayoutTrailingBlock
)

func (l SidecarLayout) String() string {
	switch l {
	case SidecarLayoutLeadingIV:
		return "leading-iv"
	case SidecarLayoutTrailingBlock:
		return "trailing-block"
	default:
		return "unknown"
	}
}

// Verifier authenticates chunks against a streaming sidecar. A message without
// a sidecar gets a nil Verifier, and integrity then rests on the whole-file
// SHA-256 checked at completion.
type Verifier struct {
	macKey  []byte
	sidecar []byte
	layout  SidecarLayout
}

// ErrSidecarMismatch means neither sidecar layout authenticated a chunk, which
// means the bytes are not what the sender sent (or the sidecar is a shape this
// code does not know). Callers fall back to a whole-file download.
var ErrSidecarMismatch = errors.New("mediastream: sidecar does not authenticate this chunk")

func NewVerifier(keys Keys, sidecar []byte) *Verifier {
	if len(sidecar) < macLength {
		return nil
	}
	return &Verifier{macKey: keys.MACKey, sidecar: sidecar}
}

// Layout reports the layout detected so far, for logging.
func (v *Verifier) Layout() SidecarLayout {
	if v == nil {
		return SidecarLayoutUnknown
	}
	return v.layout
}

// Verify authenticates chunk index. leading is the 16 bytes before the chunk,
// body is the chunk's own ciphertext, and trailing is the first block after it
// (empty for the last chunk, which has nothing following it).
func (v *Verifier) Verify(index int, leading, body, trailing []byte) error {
	if v == nil {
		return nil
	}
	offset := index * macLength
	if offset+macLength > len(v.sidecar) {
		// A sidecar that does not cover this chunk cannot condemn it; the
		// whole-file hash still has the last word.
		return nil
	}
	want := v.sidecar[offset : offset+macLength]

	switch v.layout {
	case SidecarLayoutLeadingIV:
		if v.matches(want, leading, body) {
			return nil
		}
		return ErrSidecarMismatch
	case SidecarLayoutTrailingBlock:
		if v.matches(want, body, trailing) {
			return nil
		}
		return ErrSidecarMismatch
	}

	// First chunk seen: detect the layout, then hold it for the rest of the file.
	if v.matches(want, leading, body) {
		v.layout = SidecarLayoutLeadingIV
		return nil
	}
	if v.matches(want, body, trailing) {
		v.layout = SidecarLayoutTrailingBlock
		return nil
	}
	return ErrSidecarMismatch
}

func (v *Verifier) matches(want, first, second []byte) bool {
	mac := hmac.New(sha256.New, v.macKey)
	mac.Write(first)
	mac.Write(second)
	return hmac.Equal(mac.Sum(nil)[:macLength], want)
}
