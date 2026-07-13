package openai

import (
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"
)

func repairOutputTextDeltaRule(state *ResponsesStreamState, payload []byte) [][]byte {
	itemID := gjson.GetBytes(payload, "item_id").String()
	if state == nil || strings.TrimSpace(itemID) == "" {
		return [][]byte{payload}
	}

	contentIndex := responseEventIndex(payload, "content_index")
	item := state.Items[itemID]
	if item != nil && item.Type != "" && item.Type != "message" {
		return [][]byte{payload}
	}

	needsItem := item == nil || !item.Added
	needsContentPart := !state.hasContentPart(itemID, contentIndex)
	syntheticCount := 0
	if needsItem {
		syntheticCount++
	}
	if needsContentPart {
		syntheticCount++
	}
	if syntheticCount == 0 {
		return [][]byte{payload}
	}

	sequences := state.syntheticSequences(syntheticCount, payload)
	events := make([][]byte, 0, syntheticCount+1)
	sequenceIndex := 0
	if needsItem {
		events = append(events, syntheticOutputItemAdded(payload, sequences[sequenceIndex]))
		sequenceIndex++
	}
	if needsContentPart {
		events = append(events, syntheticContentPartAdded(payload, sequences[sequenceIndex]))
	}
	return append(events, payload)
}

func syntheticOutputItemAdded(deltaPayload []byte, sequenceNumber int) []byte {
	event := struct {
		Type        string `json:"type"`
		OutputIndex int    `json:"output_index"`
		Item        struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Status  string `json:"status"`
			Role    string `json:"role"`
			Content []any  `json:"content"`
		} `json:"item"`
		SequenceNumber int `json:"sequence_number"`
	}{
		Type:           "response.output_item.added",
		OutputIndex:    responseEventIndex(deltaPayload, "output_index"),
		SequenceNumber: sequenceNumber,
	}
	event.Item.ID = gjson.GetBytes(deltaPayload, "item_id").String()
	event.Item.Type = "message"
	event.Item.Status = "in_progress"
	event.Item.Role = "assistant"
	event.Item.Content = []any{}
	payload, _ := json.Marshal(event)
	return payload
}

func syntheticContentPartAdded(deltaPayload []byte, sequenceNumber int) []byte {
	event := struct {
		Type         string `json:"type"`
		ItemID       string `json:"item_id"`
		OutputIndex  int    `json:"output_index"`
		ContentIndex int    `json:"content_index"`
		Part         struct {
			Type        string `json:"type"`
			Annotations []any  `json:"annotations"`
			Logprobs    []any  `json:"logprobs"`
			Text        string `json:"text"`
		} `json:"part"`
		SequenceNumber int `json:"sequence_number"`
	}{
		Type:           "response.content_part.added",
		ItemID:         gjson.GetBytes(deltaPayload, "item_id").String(),
		OutputIndex:    responseEventIndex(deltaPayload, "output_index"),
		ContentIndex:   responseEventIndex(deltaPayload, "content_index"),
		SequenceNumber: sequenceNumber,
	}
	event.Part.Type = "output_text"
	event.Part.Annotations = []any{}
	event.Part.Logprobs = []any{}
	payload, _ := json.Marshal(event)
	return payload
}

func responseEventIndex(payload []byte, path string) int {
	result := gjson.GetBytes(payload, path)
	if !result.Exists() {
		return 0
	}
	return int(result.Int())
}
