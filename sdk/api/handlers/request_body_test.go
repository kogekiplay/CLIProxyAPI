package handlers

import (
	"bytes"
	"testing"

	"github.com/klauspost/compress/zstd"
)

var benchmarkDecodedRequestBody []byte

func TestDecodeZstdRequestBody(t *testing.T) {
	payload := bytes.Repeat([]byte(`{"model":"gpt-5.6-sol","input":"hello"}`), 128)
	compressed := encodeZstdRequestBody(t, payload)

	decoded, err := decodeZstdRequestBody(compressed)
	if err != nil {
		t.Fatalf("decodeZstdRequestBody: %v", err)
	}
	if !bytes.Equal(decoded, payload) {
		t.Fatal("decoded request body differs from original payload")
	}
}

func TestDecodeZstdRequestBodyRejectsMalformedPayload(t *testing.T) {
	if _, err := decodeZstdRequestBody([]byte("not-zstd")); err == nil {
		t.Fatal("expected malformed zstd payload to fail")
	}
}

func BenchmarkDecodeZstdRequestBody(b *testing.B) {
	payload := bytes.Repeat([]byte(`{"type":"message","role":"user","content":"benchmark"}`), 4096)
	compressed := encodeZstdRequestBody(b, payload)

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for range b.N {
		decoded, err := decodeZstdRequestBody(compressed)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkDecodedRequestBody = decoded
	}
}

func encodeZstdRequestBody(tb testing.TB, payload []byte) []byte {
	tb.Helper()
	var compressed bytes.Buffer
	encoder, err := zstd.NewWriter(&compressed)
	if err != nil {
		tb.Fatalf("zstd.NewWriter: %v", err)
	}
	if _, err = encoder.Write(payload); err != nil {
		tb.Fatalf("zstd write: %v", err)
	}
	if err = encoder.Close(); err != nil {
		tb.Fatalf("zstd close: %v", err)
	}
	return bytes.Clone(compressed.Bytes())
}
