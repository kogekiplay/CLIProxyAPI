package openai

import (
	"bytes"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// OutputItemState tracks one Responses API output item and its content lifecycle.
type OutputItemState struct {
	ID               string
	Type             string
	Added            bool
	Done             bool
	ContentPartAdded bool
	OutputIndex      int
	HasOutputIndex   bool
	contentParts     map[int]struct{}
}

// ResponsesStreamState tracks output items and sequence numbers for one SSE stream.
type ResponsesStreamState struct {
	Items          map[string]*OutputItemState
	SequenceNumber int
	Completed      bool

	sequenceSeen         bool
	outputItems          map[int][]byte
	outputOrder          []int
	unindexedOutputItems [][]byte
}

func newResponsesStreamState() *ResponsesStreamState {
	return &ResponsesStreamState{Items: make(map[string]*OutputItemState)}
}

func (s *ResponsesStreamState) observeEvent(payload []byte) {
	if s == nil {
		return
	}
	switch responseEventType(payload) {
	case "response.output_item.added":
		s.recordOutputItemAdded(payload)
	case "response.content_part.added":
		s.recordContentPartAdded(payload)
	case "response.output_item.done":
		s.recordOutputItemDone(payload)
	case "response.completed":
		s.close()
	}
	s.observeSequence(payload)
}

func (s *ResponsesStreamState) ensureItem(itemID string) *OutputItemState {
	if s == nil {
		return nil
	}
	if s.Items == nil {
		s.Items = make(map[string]*OutputItemState)
	}
	if strings.TrimSpace(itemID) == "" {
		return nil
	}
	if item := s.Items[itemID]; item != nil {
		return item
	}
	item := &OutputItemState{ID: itemID, contentParts: make(map[int]struct{})}
	s.Items[itemID] = item
	return item
}

func (s *ResponsesStreamState) recordOutputItemAdded(payload []byte) {
	itemID := gjson.GetBytes(payload, "item.id").String()
	item := s.ensureItem(itemID)
	if item == nil {
		return
	}
	if itemType := strings.TrimSpace(gjson.GetBytes(payload, "item.type").String()); itemType != "" {
		item.Type = itemType
	}
	if outputIndex := gjson.GetBytes(payload, "output_index"); outputIndex.Exists() {
		item.OutputIndex = int(outputIndex.Int())
		item.HasOutputIndex = true
	}
	item.Added = true
}

func (s *ResponsesStreamState) recordContentPartAdded(payload []byte) {
	itemID := gjson.GetBytes(payload, "item_id").String()
	item := s.ensureItem(itemID)
	if item == nil {
		return
	}
	contentIndex := 0
	if result := gjson.GetBytes(payload, "content_index"); result.Exists() {
		contentIndex = int(result.Int())
	}
	if item.contentParts == nil {
		item.contentParts = make(map[int]struct{})
	}
	item.contentParts[contentIndex] = struct{}{}
	item.ContentPartAdded = true
}

func (s *ResponsesStreamState) hasContentPart(itemID string, contentIndex int) bool {
	if s == nil || s.Items == nil {
		return false
	}
	item := s.Items[itemID]
	if item == nil || item.contentParts == nil {
		return false
	}
	_, ok := item.contentParts[contentIndex]
	return ok
}

func (s *ResponsesStreamState) recordOutputItemDone(payload []byte) {
	itemResult := gjson.GetBytes(payload, "item")
	if !itemResult.Exists() || !itemResult.IsObject() || itemResult.Get("type").String() == "" {
		return
	}

	itemID := itemResult.Get("id").String()
	item := s.Items[itemID]
	if item != nil {
		if itemType := strings.TrimSpace(itemResult.Get("type").String()); itemType != "" {
			item.Type = itemType
		}
		item.Done = true
	}

	if outputIndex := gjson.GetBytes(payload, "output_index"); outputIndex.Exists() {
		index := int(outputIndex.Int())
		if s.outputItems == nil {
			s.outputItems = make(map[int][]byte)
		}
		if _, exists := s.outputItems[index]; !exists {
			s.outputOrder = append(s.outputOrder, index)
		}
		s.outputItems[index] = append([]byte(nil), itemResult.Raw...)
		if item != nil {
			item.OutputIndex = index
			item.HasOutputIndex = true
		}
		delete(s.Items, itemID)
		return
	}

	s.unindexedOutputItems = append(s.unindexedOutputItems, append([]byte(nil), itemResult.Raw...))
	delete(s.Items, itemID)
}

func (s *ResponsesStreamState) observeSequence(payload []byte) {
	if s == nil {
		return
	}
	sequenceNumber, ok := responseSequenceNumber(payload)
	if !ok {
		return
	}
	if !s.sequenceSeen || sequenceNumber > s.SequenceNumber {
		s.SequenceNumber = sequenceNumber
	}
	s.sequenceSeen = true
}

func (s *ResponsesStreamState) syntheticSequences(count int, upstreamPayload []byte) []*int {
	if s == nil || count <= 0 {
		return nil
	}

	start := 0
	if s.sequenceSeen {
		start = s.SequenceNumber + 1
	}
	if upstreamSequence, ok := responseSequenceNumber(upstreamPayload); ok {
		start = upstreamSequence - count
		minimum := 0
		if s.sequenceSeen {
			minimum = s.SequenceNumber + 1
		}
		if start < minimum {
			// The original upstream event cannot be renumbered. Omit synthetic
			// sequence numbers when no integer range preserves output ordering.
			return make([]*int, count)
		}
	}

	sequences := make([]*int, count)
	for i := range sequences {
		sequenceNumber := start + i
		sequences[i] = &sequenceNumber
	}
	return sequences
}

func responseSequenceNumber(payload []byte) (int, bool) {
	result := gjson.GetBytes(payload, "sequence_number")
	if !result.Exists() || result.Type != gjson.Number {
		return 0, false
	}
	return int(result.Int()), true
}

func (s *ResponsesStreamState) close() {
	if s == nil {
		return
	}
	for _, item := range s.Items {
		if item != nil {
			item.Done = true
		}
	}
	s.Items = nil
	s.outputItems = nil
	s.outputOrder = nil
	s.unindexedOutputItems = nil
	s.Completed = true
}

func (s *ResponsesStreamState) repairCompletedPayload(payload []byte) []byte {
	if s == nil || (len(s.outputOrder) == 0 && len(s.unindexedOutputItems) == 0) {
		return payload
	}
	output := gjson.GetBytes(payload, "response.output")
	if output.Exists() && (!output.IsArray() || len(output.Array()) > 0) {
		return payload
	}

	var outputJSON bytes.Buffer
	outputJSON.WriteByte('[')
	indexes := append([]int(nil), s.outputOrder...)
	sort.Ints(indexes)
	written := 0
	for _, index := range indexes {
		item, ok := s.outputItems[index]
		if !ok {
			continue
		}
		if written > 0 {
			outputJSON.WriteByte(',')
		}
		outputJSON.Write(item)
		written++
	}
	for _, item := range s.unindexedOutputItems {
		if written > 0 {
			outputJSON.WriteByte(',')
		}
		outputJSON.Write(item)
		written++
	}
	outputJSON.WriteByte(']')

	repaired, err := sjson.SetRawBytes(payload, "response.output", outputJSON.Bytes())
	if err != nil {
		return payload
	}
	return repaired
}
