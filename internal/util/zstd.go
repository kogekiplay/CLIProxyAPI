package util

import (
	"sync"

	"github.com/klauspost/compress/zstd"
)

var sharedZstdDecoder = sync.OnceValues(func() (*zstd.Decoder, error) {
	return zstd.NewReader(nil)
})

// DecodeZstd decodes a complete zstd payload with a decoder that supports
// concurrent DecodeAll calls. The returned slice is owned by the caller.
func DecodeZstd(payload []byte) ([]byte, error) {
	decoder, err := sharedZstdDecoder()
	if err != nil {
		return nil, err
	}
	return decoder.DecodeAll(payload, nil)
}
