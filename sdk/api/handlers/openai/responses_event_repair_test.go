package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestResponsesEventRepairerPreservesCompleteTextLifecycle(t *testing.T) {
	repairer := newResponsesEventRepairer()
	events := [][]byte{
		[]byte(`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg-1","type":"message","status":"in_progress","role":"assistant","content":[]},"sequence_number":1}`),
		[]byte(`{"type":"response.content_part.added","item_id":"msg-1","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]},"sequence_number":2}`),
		[]byte(`{"type":"response.output_text.delta","item_id":"msg-1","output_index":0,"content_index":0,"delta":"hello","sequence_number":3}`),
	}

	for _, event := range events {
		got := repairer.repair(event)
		if len(got) != 1 {
			t.Fatalf("expected complete lifecycle event to pass through, got %d events for %s", len(got), event)
		}
		if !bytes.Equal(got[0], event) {
			t.Fatalf("expected upstream event to remain byte-for-byte unchanged.\nGot:  %s\nWant: %s", got[0], event)
		}
	}
}

func TestResponsesEventRepairerSynthesizesMissingTextLifecycle(t *testing.T) {
	repairer := newResponsesEventRepairer()
	repairer.repair([]byte(`{"type":"response.created","response":{"id":"resp-1"},"sequence_number":10}`))
	delta := []byte(`{"type":"response.output_text.delta","item_id":"msg-1","output_index":2,"content_index":1,"delta":"hello","sequence_number":13}`)

	got := repairer.repair(delta)
	assertResponsesEventTypes(t, got,
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
	)

	if !bytes.Equal(got[2], delta) {
		t.Fatalf("expected original delta to remain unchanged.\nGot:  %s\nWant: %s", got[2], delta)
	}
	if itemID := gjson.GetBytes(got[0], "item.id").String(); itemID != "msg-1" {
		t.Fatalf("expected synthetic item msg-1, got %q", itemID)
	}
	if outputIndex := gjson.GetBytes(got[0], "output_index").Int(); outputIndex != 2 {
		t.Fatalf("expected synthetic output index 2, got %d", outputIndex)
	}
	if sequence := gjson.GetBytes(got[0], "sequence_number").Int(); sequence != 11 {
		t.Fatalf("expected first synthetic sequence 11, got %d", sequence)
	}
	if itemID := gjson.GetBytes(got[1], "item_id").String(); itemID != "msg-1" {
		t.Fatalf("expected synthetic content owner msg-1, got %q", itemID)
	}
	if contentIndex := gjson.GetBytes(got[1], "content_index").Int(); contentIndex != 1 {
		t.Fatalf("expected synthetic content index 1, got %d", contentIndex)
	}
	if sequence := gjson.GetBytes(got[1], "sequence_number").Int(); sequence != 12 {
		t.Fatalf("expected second synthetic sequence 12, got %d", sequence)
	}
}

func TestResponsesEventRepairerSynthesizesOnlyMissingContentPart(t *testing.T) {
	repairer := newResponsesEventRepairer()
	itemAdded := []byte(`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg-1","type":"message","content":[]},"sequence_number":1}`)
	if got := repairer.repair(itemAdded); len(got) != 1 || !bytes.Equal(got[0], itemAdded) {
		t.Fatalf("expected upstream item to pass through unchanged, got %q", got)
	}
	delta := []byte(`{"type":"response.output_text.delta","item_id":"msg-1","output_index":0,"content_index":0,"delta":"hello","sequence_number":3}`)

	got := repairer.repair(delta)
	assertResponsesEventTypes(t, got,
		"response.content_part.added",
		"response.output_text.delta",
	)
	if sequence := gjson.GetBytes(got[0], "sequence_number").Int(); sequence != 2 {
		t.Fatalf("expected synthetic content sequence 2, got %d", sequence)
	}
	if !bytes.Equal(got[1], delta) {
		t.Fatalf("expected original delta to remain unchanged.\nGot:  %s\nWant: %s", got[1], delta)
	}
}

func TestResponsesEventRepairerTracksInterleavedItemsIndependently(t *testing.T) {
	repairer := newResponsesEventRepairer()
	reasoningAdded := []byte(`{"type":"response.output_item.added","output_index":0,"item":{"id":"rs-1","type":"reasoning","summary":[]},"sequence_number":0}`)
	if got := repairer.repair(reasoningAdded); len(got) != 1 || !bytes.Equal(got[0], reasoningAdded) {
		t.Fatalf("expected reasoning item to pass through unchanged, got %q", got)
	}

	firstMessageDelta := []byte(`{"type":"response.output_text.delta","item_id":"msg-1","output_index":1,"content_index":0,"delta":"hello","sequence_number":3}`)
	got := repairer.repair(firstMessageDelta)
	assertResponsesEventTypes(t, got,
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
	)
	if owner := gjson.GetBytes(got[0], "item.id").String(); owner != "msg-1" {
		t.Fatalf("expected repair to target msg-1, got %q", owner)
	}
	if !bytes.Equal(got[2], firstMessageDelta) {
		t.Fatalf("expected message delta to remain unchanged, got %s", got[2])
	}

	toolAdded := []byte(`{"type":"response.output_item.added","output_index":2,"item":{"id":"tool-1","type":"function_call","name":"shell","arguments":""},"sequence_number":4}`)
	if got := repairer.repair(toolAdded); len(got) != 1 || !bytes.Equal(got[0], toolAdded) {
		t.Fatalf("expected tool item to pass through unchanged, got %q", got)
	}

	secondMessageDelta := []byte(`{"type":"response.output_text.delta","item_id":"msg-1","output_index":1,"content_index":0,"delta":" world","sequence_number":5}`)
	if got := repairer.repair(secondMessageDelta); len(got) != 1 || !bytes.Equal(got[0], secondMessageDelta) {
		t.Fatalf("expected subsequent message delta to use msg-1 state only, got %q", got)
	}
	toolDelta := []byte(`{"type":"response.function_call_arguments.delta","item_id":"tool-1","output_index":2,"delta":"{}","sequence_number":6}`)
	if got := repairer.repair(toolDelta); len(got) != 1 || !bytes.Equal(got[0], toolDelta) {
		t.Fatalf("expected tool delta to pass through unchanged, got %q", got)
	}

	if repairer.state.Items["rs-1"].Type != "reasoning" {
		t.Fatalf("reasoning item was corrupted: %#v", repairer.state.Items["rs-1"])
	}
	if repairer.state.Items["tool-1"].Type != "function_call" {
		t.Fatalf("tool item was corrupted: %#v", repairer.state.Items["tool-1"])
	}
	if repairer.state.Items["msg-1"].Type != "message" {
		t.Fatalf("message item was not tracked independently: %#v", repairer.state.Items)
	}
}

func TestResponsesEventRepairerRepairsFinalMessageAfterReasoning(t *testing.T) {
	repairer := newResponsesEventRepairer()
	reasoningEvents := [][]byte{
		[]byte(`{"type":"response.output_item.added","output_index":0,"item":{"id":"rs-1","type":"reasoning","summary":[]},"sequence_number":0}`),
		[]byte(`{"type":"response.reasoning_summary_text.delta","item_id":"rs-1","output_index":0,"summary_index":0,"delta":"thinking","sequence_number":1}`),
		[]byte(`{"type":"response.output_item.done","output_index":0,"item":{"id":"rs-1","type":"reasoning","summary":[]},"sequence_number":2}`),
	}
	for _, event := range reasoningEvents {
		got := repairer.repair(event)
		if len(got) != 1 || !bytes.Equal(got[0], event) {
			t.Fatalf("expected reasoning event to pass through unchanged, got %q for %s", got, event)
		}
	}
	if _, exists := repairer.state.Items["rs-1"]; exists {
		t.Fatalf("expected completed reasoning item to leave active state: %#v", repairer.state.Items["rs-1"])
	}

	delta := []byte(`{"type":"response.output_text.delta","item_id":"msg-1","output_index":1,"content_index":0,"delta":"final answer","sequence_number":3}`)
	got := repairer.repair(delta)
	assertResponsesEventTypes(t, got,
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
	)
	if owner := gjson.GetBytes(got[0], "item.id").String(); owner != "msg-1" {
		t.Fatalf("expected final message owner msg-1, got %q", owner)
	}
	if !bytes.Equal(got[2], delta) {
		t.Fatalf("expected final message delta to remain unchanged, got %s", got[2])
	}
	if item := repairer.state.Items["msg-1"]; item == nil || item.Type != "message" {
		t.Fatalf("expected independent final message state, got %#v", item)
	}
}

func TestResponsesEventRepairerLeavesToolCallFlowsUntouched(t *testing.T) {
	tests := []struct {
		name      string
		itemID    string
		itemType  string
		deltaType string
	}{
		{name: "function call", itemID: "tool-1", itemType: "function_call", deltaType: "response.function_call_arguments.delta"},
		{name: "custom tool call", itemID: "custom-1", itemType: "custom_tool_call", deltaType: "response.custom_tool_call_input.delta"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repairer := newResponsesEventRepairer()
			toolAdded := mustMarshalResponsesTestEvent(t, map[string]any{
				"type":         "response.output_item.added",
				"output_index": 0,
				"item": map[string]any{
					"id":   tt.itemID,
					"type": tt.itemType,
					"name": "shell",
				},
				"sequence_number": 1,
			})
			toolDelta := mustMarshalResponsesTestEvent(t, map[string]any{
				"type":            tt.deltaType,
				"item_id":         tt.itemID,
				"output_index":    0,
				"delta":           `{"cmd":"pwd"}`,
				"sequence_number": 2,
			})

			for _, event := range [][]byte{toolAdded, toolDelta} {
				got := repairer.repair(event)
				if len(got) != 1 || !bytes.Equal(got[0], event) {
					t.Fatalf("expected tool event to pass through unchanged, got %q for %s", got, event)
				}
			}
			if item := repairer.state.Items[tt.itemID]; item == nil || item.Type != tt.itemType || !item.Added {
				t.Fatalf("expected tool state to remain unpolluted, got %#v", item)
			}
			if _, exists := repairer.state.Items["msg-1"]; exists {
				t.Fatalf("tool flow unexpectedly created a message state: %#v", repairer.state.Items["msg-1"])
			}
		})
	}
}

func TestResponsesEventRepairerDoesNotRepeatSyntheticLifecycle(t *testing.T) {
	repairer := newResponsesEventRepairer()
	firstDelta := []byte(`{"type":"response.output_text.delta","item_id":"msg-1","output_index":0,"content_index":0,"delta":"hello"}`)
	secondDelta := []byte(`{"type":"response.output_text.delta","item_id":"msg-1","output_index":0,"content_index":0,"delta":" world"}`)
	thirdDelta := []byte(`{"type":"response.output_text.delta","item_id":"msg-1","output_index":0,"content_index":0,"delta":"!"}`)

	first := repairer.repair(firstDelta)
	assertResponsesEventTypes(t, first,
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
	)
	second := repairer.repair(secondDelta)
	if len(second) != 1 || !bytes.Equal(second[0], secondDelta) {
		t.Fatalf("expected repeated delta to pass through without duplicate lifecycle events, got %q", second)
	}
	third := repairer.repair(thirdDelta)
	if len(third) != 1 || !bytes.Equal(third[0], thirdDelta) {
		t.Fatalf("expected third delta to pass through without duplicate lifecycle events, got %q", third)
	}
}

func TestResponsesEventRepairerResetsDoneItemBeforeSameIDDelta(t *testing.T) {
	repairer := newResponsesEventRepairer()
	itemAdded := []byte(`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg-1","type":"message","content":[]},"sequence_number":1}`)
	itemDone := []byte(`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg-1","type":"message","status":"completed","content":[]},"sequence_number":2}`)
	repairer.repair(itemAdded)
	previous := repairer.state.Items["msg-1"]
	repairer.repair(itemDone)
	if previous == nil || !previous.Done {
		t.Fatalf("expected original item state to be done, got %#v", previous)
	}
	if _, exists := repairer.state.Items["msg-1"]; exists {
		t.Fatalf("expected done item to be removed from active state: %#v", repairer.state.Items["msg-1"])
	}

	delta := []byte(`{"type":"response.output_text.delta","item_id":"msg-1","output_index":1,"content_index":0,"delta":"new lifecycle","sequence_number":5}`)
	got := repairer.repair(delta)
	assertResponsesEventTypes(t, got,
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
	)
	current := repairer.state.Items["msg-1"]
	if current == nil || current == previous {
		t.Fatalf("expected a fresh state for reused item ID, previous=%p current=%p", previous, current)
	}
	if !current.Added || current.Done || !current.ContentPartAdded {
		t.Fatalf("unexpected fresh item state: %#v", current)
	}
	if !bytes.Equal(got[2], delta) {
		t.Fatalf("expected original delta to remain unchanged, got %s", got[2])
	}
}

func TestResponsesEventRepairerRemovesItemStateOnDone(t *testing.T) {
	repairer := newResponsesEventRepairer()
	events := [][]byte{
		[]byte(`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg-1","type":"message","content":[]},"sequence_number":1}`),
		[]byte(`{"type":"response.content_part.added","item_id":"msg-1","output_index":0,"content_index":0,"part":{"type":"output_text","text":""},"sequence_number":2}`),
		[]byte(`{"type":"response.output_text.delta","item_id":"msg-1","output_index":0,"content_index":0,"delta":"hello","sequence_number":3}`),
	}
	for _, event := range events {
		got := repairer.repair(event)
		if len(got) != 1 || !bytes.Equal(got[0], event) {
			t.Fatalf("expected normal item event to pass through unchanged, got %q for %s", got, event)
		}
	}
	item := repairer.state.Items["msg-1"]
	done := []byte(`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg-1","type":"message","status":"completed","content":[]},"sequence_number":4}`)
	if got := repairer.repair(done); len(got) != 1 || !bytes.Equal(got[0], done) {
		t.Fatalf("expected done event to pass through unchanged, got %q", got)
	}
	if item == nil || !item.Done {
		t.Fatalf("expected prior item state to be marked done, got %#v", item)
	}
	if _, exists := repairer.state.Items["msg-1"]; exists {
		t.Fatalf("expected done item to be removed from active state: %#v", repairer.state.Items["msg-1"])
	}
}

func TestResponsesEventRepairerSyntheticSequenceFollowsObservedUpstream(t *testing.T) {
	repairer := newResponsesEventRepairer()
	repairer.repair([]byte(`{"type":"response.created","response":{"id":"resp-1"},"sequence_number":100}`))
	delta := []byte(`{"type":"response.output_text.delta","item_id":"msg-1","output_index":0,"content_index":0,"delta":"hello"}`)

	got := repairer.repair(delta)
	assertResponsesEventTypes(t, got,
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
	)
	if sequence := gjson.GetBytes(got[0], "sequence_number").Int(); sequence != 101 {
		t.Fatalf("expected synthetic item sequence 101, got %d", sequence)
	}
	if sequence := gjson.GetBytes(got[1], "sequence_number").Int(); sequence != 102 {
		t.Fatalf("expected synthetic content sequence 102, got %d", sequence)
	}
}

func TestResponsesEventRepairerSyntheticSequencePreservesOutputOrder(t *testing.T) {
	repairer := newResponsesEventRepairer()
	delta := []byte(`{"type":"response.output_text.delta","item_id":"msg-1","output_index":0,"content_index":0,"delta":"hello","sequence_number":100}`)

	got := repairer.repair(delta)
	assertResponsesEventTypes(t, got,
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
	)
	if sequence := gjson.GetBytes(got[0], "sequence_number").Int(); sequence != 98 {
		t.Fatalf("expected synthetic item sequence 98, got %d", sequence)
	}
	if sequence := gjson.GetBytes(got[1], "sequence_number").Int(); sequence != 99 {
		t.Fatalf("expected synthetic content sequence 99, got %d", sequence)
	}
	if !bytes.Equal(got[2], delta) {
		t.Fatalf("expected upstream sequence to remain unchanged, got %s", got[2])
	}
}

func TestResponsesEventRepairerOmitsSyntheticSequenceWithoutAvailableRange(t *testing.T) {
	repairer := newResponsesEventRepairer()
	repairer.repair([]byte(`{"type":"response.created","response":{"id":"resp-1"},"sequence_number":99}`))
	delta := []byte(`{"type":"response.output_text.delta","item_id":"msg-1","output_index":0,"content_index":0,"delta":"hello","sequence_number":100}`)

	got := repairer.repair(delta)
	assertResponsesEventTypes(t, got,
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
	)
	if gjson.GetBytes(got[0], "sequence_number").Exists() || gjson.GetBytes(got[1], "sequence_number").Exists() {
		t.Fatalf("expected synthetic sequence numbers to be omitted when no ordered range exists: %q", got[:2])
	}
	if !bytes.Equal(got[2], delta) {
		t.Fatalf("expected upstream delta to remain unchanged, got %s", got[2])
	}
}

func TestResponsesEventRepairerTreatsItemIDAsOpaque(t *testing.T) {
	repairer := newResponsesEventRepairer()
	delta := []byte(`{"type":"response.output_text.delta","item_id":" msg-1 ","output_index":0,"content_index":0,"delta":"hello"}`)

	got := repairer.repair(delta)
	assertResponsesEventTypes(t, got,
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
	)
	if itemID := gjson.GetBytes(got[0], "item.id").String(); itemID != " msg-1 " {
		t.Fatalf("expected synthetic item to preserve opaque ID, got %q", itemID)
	}
	if itemID := gjson.GetBytes(got[1], "item_id").String(); itemID != " msg-1 " {
		t.Fatalf("expected synthetic content part to preserve opaque ID, got %q", itemID)
	}
	if !bytes.Equal(got[2], delta) {
		t.Fatalf("expected original delta to remain unchanged.\nGot:  %s\nWant: %s", got[2], delta)
	}
}

func TestResponsesEventRepairerClosesStateOnCompleted(t *testing.T) {
	repairer := newResponsesEventRepairer()
	repairer.repair([]byte(`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg-1","type":"message","content":[]}}`))
	repairer.repair([]byte(`{"type":"response.content_part.added","item_id":"msg-1","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`))
	repairer.repair([]byte(`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg-1","type":"message","status":"completed","content":[]}}`))
	repairer.repair([]byte(`{"type":"response.completed","response":{"id":"resp-1","output":[]}}`))

	if !repairer.state.Completed {
		t.Fatal("expected completed event to close stream state")
	}
	if len(repairer.state.Items) != 0 || repairer.state.outputItems != nil || repairer.state.outputOrder != nil || repairer.state.unindexedOutputItems != nil {
		t.Fatalf("expected completed event to release stream state: %#v", repairer.state)
	}

	afterCompleted := []byte(`{"type":"response.output_text.delta","item_id":"msg-2","output_index":1,"content_index":0,"delta":"ignored"}`)
	got := repairer.repair(afterCompleted)
	if len(got) != 1 || !bytes.Equal(got[0], afterCompleted) {
		t.Fatalf("expected post-completed event to pass through without reopening state, got %q", got)
	}
	if len(repairer.state.Items) != 0 {
		t.Fatalf("expected completed stream to remain closed, got %#v", repairer.state.Items)
	}
}

func FuzzResponsesEventRepairerMaintainsTextDeltaOwner(f *testing.F) {
	f.Add("msg-1", uint8(0))
	f.Add("msg-2", uint8(7))
	f.Add(" mixed-id ", uint8(31))

	f.Fuzz(func(t *testing.T, rawItemID string, mode uint8) {
		itemID := strings.TrimSpace(rawItemID)
		if itemID == "" {
			itemID = "msg-fuzz"
		}
		if len(itemID) > 128 {
			itemID = itemID[:128]
		}

		repairer := newResponsesEventRepairer()
		ownerSeen := make(map[string]bool)
		contentSeen := make(map[string]bool)
		process := func(payload []byte) {
			for _, event := range repairer.repair(payload) {
				eventType := gjson.GetBytes(event, "type").String()
				switch eventType {
				case "response.output_item.added":
					ownerSeen[gjson.GetBytes(event, "item.id").String()] = true
				case "response.content_part.added":
					key := responseContentKey(
						gjson.GetBytes(event, "item_id").String(),
						int(gjson.GetBytes(event, "content_index").Int()),
					)
					contentSeen[key] = true
				case "response.output_text.delta":
					owner := gjson.GetBytes(event, "item_id").String()
					contentIndex := int(gjson.GetBytes(event, "content_index").Int())
					if !ownerSeen[owner] {
						t.Fatalf("output_text.delta emitted without owner %q: %s", owner, event)
					}
					if !contentSeen[responseContentKey(owner, contentIndex)] {
						t.Fatalf("output_text.delta emitted without content part %q/%d: %s", owner, contentIndex, event)
					}
				}
			}
		}

		itemAdded := mustMarshalResponsesTestEvent(t, map[string]any{
			"type":         "response.output_item.added",
			"output_index": 1,
			"item": map[string]any{
				"id":      itemID,
				"type":    "message",
				"content": []any{},
			},
		})
		contentAdded := mustMarshalResponsesTestEvent(t, map[string]any{
			"type":          "response.content_part.added",
			"item_id":       itemID,
			"output_index":  1,
			"content_index": 0,
			"part":          map[string]any{"type": "output_text", "text": ""},
		})
		if mode&1 != 0 {
			process(itemAdded)
		}
		if mode&2 != 0 {
			process(contentAdded)
		}
		if mode&4 != 0 && mode&1 == 0 {
			process(itemAdded)
		}

		deltaCount := int(mode%4) + 1
		for index := 0; index < deltaCount; index++ {
			process(mustMarshalResponsesTestEvent(t, map[string]any{
				"type":          "response.output_text.delta",
				"item_id":       itemID,
				"output_index":  1,
				"content_index": 0,
				"delta":         fmt.Sprintf("chunk-%d", index),
			}))
		}

		process([]byte(`{"type":"response.completed","response":{"id":"resp-fuzz","output":[]}}`))
		if !repairer.state.Completed {
			t.Fatal("expected completed event to close fuzzed stream")
		}
		for id, item := range repairer.state.Items {
			if item != nil && !item.Done {
				t.Fatalf("expected item %q to be closed after completed", id)
			}
		}
	})
}

func assertResponsesEventTypes(t *testing.T, events [][]byte, want ...string) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("expected %d events, got %d: %q", len(want), len(events), events)
	}
	for index, eventType := range want {
		if got := gjson.GetBytes(events[index], "type").String(); got != eventType {
			t.Fatalf("event %d type mismatch: got %q, want %q in %s", index, got, eventType, events[index])
		}
	}
}

func mustMarshalResponsesTestEvent(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal test event: %v", err)
	}
	return payload
}

func responseContentKey(itemID string, contentIndex int) string {
	return fmt.Sprintf("%s/%d", itemID, contentIndex)
}
