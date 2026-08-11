package wa

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// WebP RIFF layout, for the chunk walk below:
//
//	"RIFF" <u32 size> "WEBP" then a sequence of chunks, each
//	<4-byte id> <u32 size> <payload, padded to even length>
//
// An extended file opens with a VP8X chunk whose first payload byte holds the
// feature flags; an animated file then carries an ANIM chunk followed by one
// ANMF chunk per frame. An ANMF payload is a 16-byte frame header (offset,
// size, duration, blend/dispose flags) followed by that frame's own image
// chunks — optionally an ALPH alpha plane, then VP8 or VP8L.
const (
	webpRIFFHeaderSize = 12
	webpVP8XFlagOffset = 20
	webpAlphaFlag      = 0x10
	webpAnimationFlag  = 0x02
	webpANMFHeaderSize = 16
)

// webpNeedsAlphaFlag reports whether a WebP file is an animation whose frames
// carry ALPH chunks while its VP8X header claims the canvas has no alpha.
//
// Encoders that build an animation by inter-frame compression — WhatsApp's
// video-to-sticker converter among them — store each frame at full canvas size
// with blend enabled and use the frame's alpha plane as a "changed pixels"
// mask: alpha 0 means "keep whatever the previous frame put here". Some of them
// then forget to set ALPHA_FLAG in VP8X, since the *canvas* is opaque once the
// frames are composited.
//
// libwebp, PIL and browsers all composite from the per-frame data and render
// these correctly. Qt's WebP handler instead decides whether to composite from
// the VP8X flag, so with the flag clear it hands back each raw frame with its
// mask still punched through — the unchanged regions arrive transparent, and an
// animated sticker renders as blocky garbage from frame 1 on. Setting the bit
// is a lossless one-byte repair that puts Qt back on its compositing path.
//
// The check is ordered to be cheap: the 21-byte header settles all but the
// affected files, and only an animation that is missing the flag pays for the
// chunk walk.
func webpNeedsAlphaFlag(reader io.ReaderAt, size int64) (bool, error) {
	header := make([]byte, webpVP8XFlagOffset+1)
	if size < int64(len(header)) {
		return false, nil
	}
	if _, err := reader.ReadAt(header, 0); err != nil {
		return false, err
	}
	if string(header[0:4]) != "RIFF" || string(header[8:12]) != "WEBP" || string(header[12:16]) != "VP8X" {
		return false, nil
	}
	flags := header[webpVP8XFlagOffset]
	if flags&webpAnimationFlag == 0 || flags&webpAlphaFlag != 0 {
		return false, nil
	}
	return webpHasFrameAlpha(reader, size)
}

// webpHasFrameAlpha reports whether any frame of an animation carries an ALPH
// chunk. It walks the chunk table by seeking over payloads, so the cost is a
// handful of 8-byte reads per frame rather than the whole file.
func webpHasFrameAlpha(reader io.ReaderAt, size int64) (bool, error) {
	chunkHeader := make([]byte, 8)
	subChunkID := make([]byte, 4)
	for offset := int64(webpRIFFHeaderSize); offset+8 <= size; {
		if _, err := reader.ReadAt(chunkHeader, offset); err != nil {
			return false, err
		}
		payloadSize := int64(binary.LittleEndian.Uint32(chunkHeader[4:8]))
		if payloadSize < 0 || offset+8+payloadSize > size {
			// Truncated or malformed tail: nothing further can be trusted.
			return false, nil
		}
		if string(chunkHeader[0:4]) == "ANMF" && payloadSize >= webpANMFHeaderSize+8 {
			if _, err := reader.ReadAt(subChunkID, offset+8+webpANMFHeaderSize); err != nil {
				return false, err
			}
			if string(subChunkID) == "ALPH" {
				return true, nil
			}
		}
		offset += 8 + payloadSize + (payloadSize & 1)
	}
	return false, nil
}

// repairWebPAlphaFlagBytes applies the repair to an in-memory file, reporting
// whether it changed anything. Used on the write paths that already hold the
// bytes, so the file lands correct instead of being rewritten a moment later.
func repairWebPAlphaFlagBytes(data []byte) bool {
	needs, err := webpNeedsAlphaFlag(bytes.NewReader(data), int64(len(data)))
	if err != nil || !needs {
		return false
	}
	data[webpVP8XFlagOffset] |= webpAlphaFlag
	return true
}

// repairWebPAlphaFlagFile applies the repair to a file already on disk,
// reporting whether it changed anything. The write is a single byte at a fixed
// offset, so there is no rewrite and no window where the file is truncated.
func repairWebPAlphaFlagFile(path string) (bool, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	needs, err := webpNeedsAlphaFlag(file, info.Size())
	if err != nil || !needs {
		return false, err
	}

	header := make([]byte, 1)
	if _, err := file.ReadAt(header, webpVP8XFlagOffset); err != nil {
		return false, err
	}
	header[0] |= webpAlphaFlag
	if _, err := file.WriteAt(header, webpVP8XFlagOffset); err != nil {
		return false, err
	}
	return true, nil
}

// repairCachedWebPAlphaFlags sweeps the sticker cache for files written before
// the repair existed. It is deliberately not gated on a "done" marker: the
// per-file cost is one 21-byte read for everything that is already correct, so
// running it on every start is cheaper than tracking state, and it also catches
// files restored from a backup or written by an older build.
func (c *Client) repairCachedWebPAlphaFlags() {
	dir := filepath.Join(c.paths.MediaCacheDir, "stickers")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Debug("scan sticker cache for webp alpha repair", "error", err)
		}
		return
	}

	repaired := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".webp" {
			continue
		}
		changed, err := repairWebPAlphaFlagFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			slog.Debug("repair cached sticker webp alpha flag", "file", entry.Name(), "error", err)
			continue
		}
		if changed {
			repaired++
		}
	}
	if repaired > 0 {
		slog.Info("repaired animated sticker webp headers", "count", repaired, "scanned", len(entries))
	}
}
