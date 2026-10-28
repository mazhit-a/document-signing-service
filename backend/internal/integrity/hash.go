package integrity

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
)

type HashingReader struct {
	r io.Reader
	h hash.Hash
	n int64
}

func NewHashingReader(r io.Reader) *HashingReader {
	return &HashingReader{r: r, h: sha256.New()}
}

func (hr *HashingReader) Read(p []byte) (int, error) {
	n, err := hr.r.Read(p)
	if n > 0 {
		hr.h.Write(p[:n])
		hr.n += int64(n)
	}
	return n, err
}

func (hr *HashingReader) Size() int64 {
	return hr.n
}

func (hr *HashingReader) Sum() string {
	return hex.EncodeToString(hr.h.Sum(nil))
}

func SHA256Reader(r io.Reader) (string, int64, error) {
	hr := NewHashingReader(r)
	if _, err := io.Copy(io.Discard, hr); err != nil {
		return "", hr.Size(), err
	}
	return hr.Sum(), hr.Size(), nil
}

func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
