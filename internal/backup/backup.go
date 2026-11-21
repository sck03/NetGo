package backup

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/scrypt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var magic = []byte("NETGO-BACKUP-1\n")

func Encrypt(plain, password []byte) ([]byte, error) {
	if len(password) < 12 {
		return nil, errors.New("backup password must contain at least 12 bytes")
	}
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		return nil, e
	}
	key, e := scrypt.Key(password, salt, 32768, 8, 1, 32)
	if e != nil {
		return nil, e
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, g.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	header := append(append(append([]byte{}, magic...), salt...), nonce...)
	return append(header, g.Seal(nil, nonce, plain, header)...), nil
}
func Decrypt(data, password []byte) ([]byte, error) {
	if len(data) < len(magic)+16+12+16 || !bytes.HasPrefix(data, magic) {
		return nil, errors.New("invalid backup")
	}
	off := len(magic)
	key, e := scrypt.Key(password, data[off:off+16], 32768, 8, 1, 32)
	if e != nil {
		return nil, e
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	n := off + 16 + g.NonceSize()
	out, e := g.Open(nil, data[off+16:n], data[n:], data[:n])
	if e != nil {
		return nil, errors.New("wrong password or damaged backup")
	}
	return out, nil
}
func Archive(roots []string) ([]byte, error) {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	seen := map[string]bool{}
	total := int64(0)
	for _, root := range roots {
		root, e := filepath.Abs(root)
		if e != nil {
			return nil, e
		}
		base := filepath.Base(root)
		if seen[base] {
			return nil, errors.New("duplicate backup root name")
		}
		seen[base] = true
		e = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				return errors.New("backup symlinks are not allowed")
			}
			if d.IsDir() {
				return nil
			}
			info, e := d.Info()
			if e != nil {
				return e
			}
			if !info.Mode().IsRegular() {
				return errors.New("backup contains nonregular file")
			}
			total += info.Size()
			if total > 256<<20 {
				return errors.New("backup exceeds 256 MB; use documented external consistent snapshot")
			}
			rel, e := filepath.Rel(root, path)
			if e != nil {
				return e
			}
			f, e := w.Create(filepath.ToSlash(filepath.Join(base, rel)))
			if e != nil {
				return e
			}
			input, e := os.Open(path)
			if e != nil {
				return e
			}
			defer input.Close()
			_, e = io.Copy(f, input)
			return e
		})
		if e != nil {
			return nil, e
		}
	}
	manifest, _ := w.Create("manifest.json")
	_ = json.NewEncoder(manifest).Encode(map[string]any{"format": 1, "roots": roots, "requires_stopped_services": true})
	if e := w.Close(); e != nil {
		return nil, e
	}
	return b.Bytes(), nil
}
func Restore(plain []byte, dest string) error {
	abs, e := filepath.Abs(dest)
	if e != nil {
		return e
	}
	if _, e = os.Lstat(abs); !os.IsNotExist(e) {
		return errors.New("restore target must not exist")
	}
	r, e := zip.NewReader(bytes.NewReader(plain), int64(len(plain)))
	if e != nil {
		return e
	}
	total := uint64(0)
	names := map[string]bool{}
	for _, f := range r.File {
		clean := filepath.Clean(filepath.FromSlash(f.Name))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || strings.ContainsAny(clean, ":\x00") || f.Mode()&os.ModeSymlink != 0 || names[clean] {
			return errors.New("unsafe archive path")
		}
		names[clean] = true
		total += f.UncompressedSize64
		if total > 256<<20 {
			return errors.New("archive expansion exceeds limit")
		}
	}
	if e = os.Mkdir(abs, 0700); e != nil {
		return e
	}
	for _, f := range r.File {
		path := filepath.Join(abs, filepath.FromSlash(f.Name))
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return e
		}
		if f.FileInfo().IsDir() {
			if e = os.MkdirAll(path, 0700); e != nil {
				return e
			}
			continue
		}
		in, e := f.Open()
		if e != nil {
			return e
		}
		out, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			in.Close()
			return e
		}
		_, e = io.Copy(out, io.LimitReader(in, 256<<20))
		in.Close()
		ce := out.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
	}
	return nil
}
