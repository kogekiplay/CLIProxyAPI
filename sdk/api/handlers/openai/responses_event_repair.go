package openai

import (
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
	if r == nil {
		return [][]byte{payload}
	}
	if r.state == nil {
		r.state = newResponsesStreamState()
	}
	if r.state.Completed {
		return [][]byte{payload}
	}

	events := [][]byte{payload}
	switch responseEventType(payload) {
	case "response.output_text.delta":
		events = repairOutputTextDeltaRule(r.state, payload)
	case "response.completed":
		events[0] = r.state.repairCompletedPayload(payload)
	}

	for _, event := range events {
		r.state.observeEvent(event)
	}
	return events
}

func responseEventType(payload []byte) string {
	return strings.TrimSpace(gjson.GetBytes(payload, "type").String())
}
