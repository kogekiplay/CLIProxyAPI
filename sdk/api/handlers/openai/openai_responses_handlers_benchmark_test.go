package openai

import (
	"encoding/json"
	"io"
	"testing"

	"github.com/tidwall/gjson"
)

var benchmarkResponsesSSEPayload []byte
var benchmarkResponsesWebsocketPayloads [][]byte
var benchmarkResponsesSSEValid bool

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

func BenchmarkResponsesSSEFramerBatchedTextStream(b *testing.B) {
	itemAdded := []byte("data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"id\":\"msg1\",\"type\":\"message\",\"status\":\"in_progress\",\"role\":\"assistant\",\"content\":[]}}\n\n")
	contentAdded := []byte("data: {\"type\":\"response.content_part.added\",\"item_id\":\"msg1\",\"output_index\":0,\"content_index\":0,\"part\":{\"type\":\"output_text\",\"text\":\"\",\"annotations\":[]}}\n\n")
	delta := []byte("data: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg1\",\"output_index\":0,\"content_index\":0,\"delta\":\"hello\"}\n\n")
	chunk := make([]byte, 0, len(itemAdded)+len(contentAdded)+100*len(delta))
	chunk = append(chunk, itemAdded...)
	chunk = append(chunk, contentAdded...)
	for i := 0; i < 100; i++ {
		chunk = append(chunk, delta...)
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(chunk)))
	b.ResetTimer()
	for range b.N {
		framer := &responsesSSEFramer{}
		framer.WriteChunk(io.Discard, chunk)
		framer.Flush(io.Discard)
	}
}

func BenchmarkResponsesSSEDataPayloadSingleLine(b *testing.B) {
	frame := []byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg1\",\"delta\":\"hello\"}\n\n")

	b.ReportAllocs()
	b.SetBytes(int64(len(frame)))
	b.ResetTimer()
	for range b.N {
		benchmarkResponsesSSEPayload, _ = responsesSSEDataPayload(frame)
	}
}

func BenchmarkResponsesSSEJSONValidation(b *testing.B) {
	payload := []byte("{\"type\":\"response.output_text.delta\",\"item_id\":\"msg1\",\"output_index\":0,\"content_index\":0,\"delta\":\"hello\"}")
	b.Run("encoding_json", func(b *testing.B) {
		for range b.N {
			benchmarkResponsesSSEValid = json.Valid(payload)
		}
	})
	b.Run("gjson", func(b *testing.B) {
		for range b.N {
			benchmarkResponsesSSEValid = gjson.ValidBytes(payload)
		}
	})
}

func BenchmarkWebsocketJSONPayloadsFromChunk(b *testing.B) {
	chunk := []byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg1\",\"delta\":\"hello\"}\n\n")
	b.ReportAllocs()
	b.SetBytes(int64(len(chunk)))
	b.ResetTimer()
	for range b.N {
		benchmarkResponsesWebsocketPayloads = websocketJSONPayloadsFromChunk(chunk)
	}
}
