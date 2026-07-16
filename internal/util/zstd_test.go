package util

import (
	"bytes"
	"sync"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestDecodeZstdConcurrent(t *testing.T) {
	payload := bytes.Repeat([]byte("concurrent zstd payload"), 1024)
	compressed := encodeZstdForTest(t, payload)

	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 16 {
				decoded, err := DecodeZstd(compressed)
				if err != nil {
					t.Errorf("DecodeZstd: %v", err)
					return
				}
				if !bytes.Equal(decoded, payload) {
					t.Error("decoded payload differs from original")
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestDecodeZstdRejectsMalformedPayload(t *testing.T) {
	if _, err := DecodeZstd([]byte("not-zstd")); err == nil {
		t.Fatal("expected malformed zstd payload to fail")
	}
}

func encodeZstdForTest(t *testing.T, payload []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	encoder, err := zstd.NewWriter(&compressed)
	if err != nil {
		t.Fatalf("zstd.NewWriter: %v", err)
	}
	if _, err = encoder.Write(payload); err != nil {
		t.Fatalf("zstd write: %v", err)
	}
	if err = encoder.Close(); err != nil {
		t.Fatalf("zstd close: %v", err)
	}
	return bytes.Clone(compressed.Bytes())
}
