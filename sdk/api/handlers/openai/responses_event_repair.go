package openai

import (
	"bytes"
	"strings"

	"github.com/tidwall/gjson"
)

type responsesEventRepairer struct {
	state *ResponsesStreamState
}

func newResponsesEventRepairer() *responsesEventRepairer {
	return &responsesEventRepairer{state: newResponsesStreamState()}
}

func (r *responsesEventRepairer) repair(payload []byte) [][]byte {
	repaired := r.repairChanged(payload)
	if len(repaired) == 0 {
		return [][]byte{payload}
	}
	return repaired
}

// repairChanged returns nil when the upstream payload can be forwarded as-is.
// The framer uses this fast path to avoid allocating a result slice per event.
func (r *responsesEventRepairer) repairChanged(payload []byte) [][]byte {
	if r == nil {
		return nil
	}
	if r.state == nil {
		r.state = newResponsesStreamState()
	}
	if r.state.Completed {
		return nil
	}

	eventType := responseEventType(payload)
	var events [][]byte
	switch eventType {
	case "response.output_text.delta":
		events = repairOutputTextDeltaRule(r.state, payload)
		if len(events) == 1 && bytes.Equal(events[0], payload) {
			r.state.observeEventType(payload, eventType)
			return nil
		}
	case "response.completed":
		repaired := r.state.repairCompletedPayload(payload)
		r.state.observeEventType(repaired, eventType)
		if bytes.Equal(repaired, payload) {
			return nil
		}
		return [][]byte{repaired}
	default:
		r.state.observeEventType(payload, eventType)
		return nil
	}

	for index, event := range events {
		if index == len(events)-1 && bytes.Equal(event, payload) {
			r.state.observeEventType(event, eventType)
		} else {
			r.state.observeEvent(event)
		}
	}
	return events
}

func responseEventType(payload []byte) string {
	return strings.TrimSpace(gjson.GetBytes(payload, "type").String())
}
