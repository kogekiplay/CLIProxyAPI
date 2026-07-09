package executor

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/tidwall/gjson"
)

func TestCodexContinuationConfigDefaultsEnabled(t *testing.T) {
	opts := codexContinuationOptionsFromConfig(&config.Config{})

	if !opts.Enabled {
		t.Fatal("expected Codex continuation to be enabled by default")
	}
	if opts.MaxContinue != 3 {
		t.Fatalf("MaxContinue = %d, want 3", opts.MaxContinue)
	}
	if opts.TruncationStep != 518 {
		t.Fatalf("TruncationStep = %d, want 518", opts.TruncationStep)
	}
	if opts.MarkerText != "Continue thinking..." {
		t.Fatalf("MarkerText = %q, want default marker", opts.MarkerText)
	}
}

func TestCodexContinuationFoldContinuesAndDropsTruncatedTentativeOutput(t *testing.T) {
	baseBody := []byte(`{"model":"gpt-5.5","stream":true,"previous_response_id":"resp-prev","input":[{"type":"message","role":"user","content":"hello"}]}`)
	first := codexContinuationTestResponse(`data: {"type":"response.created","response":{"id":"resp-1","status":"in_progress","output":[]}}

data: {"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs-1","summary":[]}}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs-1","summary":[],"encrypted_content":"enc-1"}}

data: {"type":"response.output_item.added","output_index":1,"item":{"type":"message","id":"msg-partial","role":"assistant","content":[]}}

data: {"type":"response.output_text.delta","output_index":1,"item_id":"msg-partial","content_index":0,"delta":"bad partial"}

data: {"type":"response.output_item.done","output_index":1,"item":{"type":"message","id":"msg-partial","role":"assistant","content":[{"type":"output_text","text":"bad partial"}]}}

data: {"type":"response.completed","response":{"id":"resp-1","status":"completed","output":[],"usage":{"input_tokens":11,"output_tokens":520,"total_tokens":531,"input_tokens_details":{"cached_tokens":3},"output_tokens_details":{"reasoning_tokens":516}}}}

data: [DONE]

`)

	var capturedPayload []byte
	openRound := func(_ context.Context, payload []byte) (*http.Response, error) {
		capturedPayload = append([]byte(nil), payload...)
		return codexContinuationTestResponse(`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs-2","summary":[]}}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs-2","summary":[],"encrypted_content":"enc-2"}}

data: {"type":"response.output_item.added","output_index":1,"item":{"type":"message","id":"msg-final","role":"assistant","content":[]}}

data: {"type":"response.output_text.delta","output_index":1,"item_id":"msg-final","content_index":0,"delta":"final answer"}

data: {"type":"response.output_item.done","output_index":1,"item":{"type":"message","id":"msg-final","role":"assistant","content":[{"type":"output_text","text":"final answer"}]}}

data: {"type":"response.completed","response":{"id":"resp-2","status":"completed","output":[],"usage":{"input_tokens":17,"output_tokens":20,"total_tokens":37,"output_tokens_details":{"reasoning_tokens":10}}}}

data: [DONE]

`), nil
	}

	var out bytes.Buffer
	result, err := foldCodexContinuationStream(context.Background(), codexContinuationOptionsFromConfig(&config.Config{}), baseBody, first, openRound, func(chunk []byte) {
		out.Write(chunk)
		out.WriteByte('\n')
	})
	if err != nil {
		t.Fatalf("foldCodexContinuationStream returned error: %v", err)
	}
	if result.Rounds != 2 {
		t.Fatalf("rounds = %d, want 2", result.Rounds)
	}
	if !result.Continued {
		t.Fatal("expected continuation to run")
	}

	body := out.String()
	if strings.Contains(body, "bad partial") {
		t.Fatalf("truncated tentative output leaked downstream:\n%s", body)
	}
	if !strings.Contains(body, "final answer") {
		t.Fatalf("final answer missing from folded output:\n%s", body)
	}
	if !strings.Contains(body, `"proxy_billed_usage"`) {
		t.Fatalf("fold metadata missing billed usage:\n%s", body)
	}

	if len(capturedPayload) == 0 {
		t.Fatal("expected continuation payload to be captured")
	}
	if gjson.GetBytes(capturedPayload, "previous_response_id").Exists() {
		t.Fatalf("previous_response_id leaked into continuation payload: %s", capturedPayload)
	}
	input := gjson.GetBytes(capturedPayload, "input")
	if !input.IsArray() || len(input.Array()) != 3 {
		t.Fatalf("continuation input len = %d, want 3: %s", len(input.Array()), capturedPayload)
	}
	if got := input.Array()[0].Get("role").String(); got != "user" {
		t.Fatalf("input[0].role = %q, want user: %s", got, capturedPayload)
	}
	if got := input.Array()[1].Get("type").String(); got != "reasoning" {
		t.Fatalf("input[1].type = %q, want reasoning: %s", got, capturedPayload)
	}
	if got := input.Array()[2].Get("phase").String(); got != "commentary" {
		t.Fatalf("input[2].phase = %q, want commentary: %s", got, capturedPayload)
	}
	if got := input.Array()[2].Get("content.0.text").String(); got != "Continue thinking..." {
		t.Fatalf("commentary marker = %q, want default: %s", got, capturedPayload)
	}
	if !codexContinuationIncludeHasEncrypted(capturedPayload) {
		t.Fatalf("continuation payload must request reasoning.encrypted_content: %s", capturedPayload)
	}
}

func codexContinuationTestResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
