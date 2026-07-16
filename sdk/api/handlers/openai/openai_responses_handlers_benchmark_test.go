package openai

import (
	"io"
	"testing"
)

func BenchmarkResponsesSSEFramerNormalTextStream(b *testing.B) {
	itemAdded := []byte("data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"id\":\"msg1\",\"type\":\"message\",\"status\":\"in_progress\",\"role\":\"assistant\",\"content\":[]}}\n\n")
	contentAdded := []byte("data: {\"type\":\"response.content_part.added\",\"item_id\":\"msg1\",\"output_index\":0,\"content_index\":0,\"part\":{\"type\":\"output_text\",\"text\":\"\",\"annotations\":[]}}\n\n")
	delta := []byte("data: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg1\",\"output_index\":0,\"content_index\":0,\"delta\":\"hello\"}\n\n")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		framer := &responsesSSEFramer{}
		framer.WriteChunk(io.Discard, itemAdded)
		framer.WriteChunk(io.Discard, contentAdded)
		for i := 0; i < 100; i++ {
			framer.WriteChunk(io.Discard, delta)
		}
		framer.Flush(io.Discard)
	}
}
