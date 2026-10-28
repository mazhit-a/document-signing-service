package integrity

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

type zeroReader struct{ remaining int64 }

func (z *zeroReader) Read(p []byte) (int, error) {
	if z.remaining <= 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > z.remaining {
		n = z.remaining
	}
	for i := int64(0); i < n; i++ {
		p[i] = 0
	}
	z.remaining -= n
	return int(n), nil
}

func TestSHA256KnownVectors(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"empty", "", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"abc", "abc", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SHA256Hex([]byte(tt.in)); got != tt.want {
				t.Fatalf("SHA256Hex = %s, want %s", got, tt.want)
			}
			got, n, err := SHA256Reader(strings.NewReader(tt.in))
			if err != nil || got != tt.want || n != int64(len(tt.in)) {
				t.Fatalf("SHA256Reader = %s,%d,%v", got, n, err)
			}
		})
	}
}

func TestHashingReaderPassesThroughAndHashes(t *testing.T) {
	in := []byte("hello world")
	hr := NewHashingReader(bytes.NewReader(in))
	out, err := io.ReadAll(hr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, in) {
		t.Fatalf("passthrough mismatch")
	}
	if hr.Size() != int64(len(in)) || hr.Sum() != SHA256Hex(in) {
		t.Fatalf("size/sum mismatch")
	}
}

func TestHashingReaderLargeStream(t *testing.T) {
	const size = 256 << 20
	sum, n, err := SHA256Reader(&zeroReader{remaining: size})
	if err != nil {
		t.Fatal(err)
	}
	if n != size {
		t.Fatalf("n = %d", n)
	}
	sum2, _, _ := SHA256Reader(&zeroReader{remaining: size})
	if sum != sum2 {
		t.Fatal("hash not deterministic")
	}
}

func TestFlippedByteChangesHash(t *testing.T) {
	orig := []byte("some document content")
	want := SHA256Hex(orig)
	for i := range orig {
		mod := append([]byte(nil), orig...)
		mod[i] ^= 0x01
		if SHA256Hex(mod) == want {
			t.Fatalf("flip at %d not detected", i)
		}
	}
}
