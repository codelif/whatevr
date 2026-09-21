package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/crypto/scrypt"

	"whatevrd/internal/app"
)

// Bundle layout (paths inside the tar):
//
//	whatevrd.db            daemon message store snapshot (VACUUM INTO)
//	session.db             WhatsApp device/session snapshot (VACUUM INTO)
//	media/...              daemon media cache (best-effort copy)
//
// An encrypted bundle is magic + salt + nonce + AES-256-GCM(ciphertext of the
// tar.gz); the key comes from scrypt(passphrase, salt).
const (
	bundleMagic   = "WVBACKUP1"
	scryptN       = 32768
	scryptR       = 8
	scryptP       = 1
	scryptKeyLen  = 32
	maxPassphrase = 1024
)

// vacuumSnapshot copies a live SQLite database to dest with VACUUM INTO: a
// transactionally consistent snapshot that works while the daemon holds the
// file open (plain file copies of a WAL database are not).
func vacuumSnapshot(ctx context.Context, srcPath, dest string) error {
	db, err := sql.Open("sqlite3", "file:"+srcPath+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `PRAGMA busy_timeout = 30000`); err != nil {
		return err
	}
	quoted := "'" + strings.ReplaceAll(dest, "'", "''") + "'"
	if _, err := db.ExecContext(ctx, `VACUUM INTO `+quoted); err != nil {
		return err
	}
	return nil
}

// Export writes a backup bundle of the daemon database, session database and
// media cache to dest. A non-empty passphrase encrypts the bundle; nil means
// a plain tar.gz. Missing optional sources (session DB on a fresh install,
// media cache) are skipped, never fatal.
func Export(ctx context.Context, paths app.Paths, dest string, passphrase []byte) (int64, error) {
	if strings.TrimSpace(dest) == "" {
		return 0, errors.New("backup destination path is required")
	}
	if !filepath.IsAbs(dest) {
		return 0, errors.New("backup destination path must be absolute")
	}
	if len(passphrase) > maxPassphrase {
		return 0, errors.New("passphrase is too long")
	}

	work, err := os.MkdirTemp("", "whatevr-backup-*")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(work)

	if _, err := os.Stat(paths.DatabasePath); err == nil {
		if err := vacuumSnapshot(ctx, paths.DatabasePath, filepath.Join(work, "whatevrd.db")); err != nil {
			return 0, fmt.Errorf("snapshot message store: %w", err)
		}
	}
	if _, err := os.Stat(paths.SessionDBPath); err == nil {
		if err := vacuumSnapshot(ctx, paths.SessionDBPath, filepath.Join(work, "session.db")); err != nil {
			return 0, fmt.Errorf("snapshot session store: %w", err)
		}
	}

	archivePath := filepath.Join(work, "bundle.tar.gz")
	if err := writeTarGzip(archivePath, work, paths.MediaCacheDir); err != nil {
		return 0, err
	}
	raw, err := os.ReadFile(archivePath)
	if err != nil {
		return 0, err
	}
	if len(passphrase) > 0 {
		raw, err = encryptBundle(raw, passphrase)
		if err != nil {
			return 0, err
		}
	}
	if err := writeFileAtomic(dest, raw, 0o600); err != nil {
		return 0, err
	}
	info, err := os.Stat(dest)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// writeTarGzip archives the snapshot files plus the media cache. Snapshot
// files sit at the tar root; media lands under media/.
func writeTarGzip(archivePath, workDir, mediaDir string) error {
	f, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	addFile := func(fsPath, tarName string, mode int64) error {
		info, err := os.Stat(fsPath)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		header := &tar.Header{
			Name:    tarName,
			Mode:    mode,
			Size:    info.Size(),
			ModTime: info.ModTime(),
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		in, err := os.Open(fsPath)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(tw, in)
		return err
	}
	for _, name := range []string{"whatevrd.db", "session.db"} {
		if _, err := os.Stat(filepath.Join(workDir, name)); err == nil {
			if err := addFile(filepath.Join(workDir, name), name, 0o600); err != nil {
				tw.Close()
				gz.Close()
				f.Close()
				return err
			}
		}
	}
	if mediaInfo, err := os.Stat(mediaDir); err == nil && mediaInfo.IsDir() {
		err := filepath.WalkDir(mediaDir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return err
			}
			rel, err := filepath.Rel(mediaDir, path)
			if err != nil {
				return err
			}
			return addFile(path, filepath.Join("media", rel), 0o600)
		})
		if err != nil {
			tw.Close()
			gz.Close()
			f.Close()
			return err
		}
	}
	if err := tw.Close(); err != nil {
		gz.Close()
		f.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// Restore extracts a bundle over the daemon's data and cache dirs. The daemon
// must be stopped first (main guards this with the process lock): restoring
// under a live writer corrupts both databases. Unknown entries are skipped so
// newer bundles restore their known files onto older daemons.
func Restore(paths app.Paths, bundlePath string, passphrase []byte) error {
	if strings.TrimSpace(bundlePath) == "" {
		return errors.New("backup bundle path is required")
	}
	raw, err := os.ReadFile(bundlePath)
	if err != nil {
		return err
	}
	if isEncryptedBundle(raw) {
		if len(passphrase) == 0 {
			return errors.New("bundle is encrypted: a passphrase is required")
		}
		raw, err = decryptBundle(raw, passphrase)
		if err != nil {
			return err
		}
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return errors.New("not a whatevr backup bundle")
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		dest, ok := restoreDest(paths, header.Name)
		if !ok {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return err
		}
		if err := writeStreamAtomic(dest, tr, 0o600); err != nil {
			return err
		}
		_ = os.Chtimes(dest, header.AccessTime, header.ModTime)
	}
	return nil
}

// restoreDest maps a tar entry to its live location. Media restores into the
// cache; databases restore over their files.
func restoreDest(paths app.Paths, name string) (string, bool) {
	clean := filepath.Clean("/" + name)[1:]
	switch {
	case clean == "whatevrd.db":
		return paths.DatabasePath, true
	case clean == "session.db":
		return paths.SessionDBPath, true
	case clean == "media" || strings.HasPrefix(clean, "media/"):
		rel := strings.TrimPrefix(clean, "media/")
		if rel == "" || rel == "." {
			return "", false
		}
		if strings.Contains(rel, "..") {
			return "", false
		}
		return filepath.Join(paths.MediaCacheDir, rel), true
	default:
		return "", false
	}
}

func writeStreamAtomic(dest string, src io.Reader, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(dest), "."+filepath.Base(dest)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, dest)
}

func isEncryptedBundle(raw []byte) bool {
	return len(raw) > len(bundleMagic) && string(raw[:len(bundleMagic)]) == bundleMagic
}

func encryptBundle(plaintext, passphrase []byte) ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := scrypt.Key(passphrase, salt, scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(bundleMagic)+len(salt)+len(nonce)+len(plaintext)+gcm.Overhead())
	out = append(out, bundleMagic...)
	out = append(out, salt...)
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, plaintext, nil)
	return out, nil
}

func decryptBundle(raw, passphrase []byte) ([]byte, error) {
	head := len(bundleMagic) + 16
	if len(raw) < head+12 {
		return nil, errors.New("backup bundle is truncated")
	}
	salt := raw[len(bundleMagic):head]
	key, err := scrypt.Key(passphrase, salt, scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := raw[head : head+gcm.NonceSize()]
	ciphertext := raw[head+gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.New("wrong passphrase or corrupted backup bundle")
	}
	return plaintext, nil
}

// DefaultDest names a timestamped bundle next to the data dir.
func DefaultDest(paths app.Paths) string {
	return filepath.Join(filepath.Dir(paths.DataDir),
		fmt.Sprintf("whatevr-backup-%s.tar.gz", time.Now().Format("20060102-150405")))
}
