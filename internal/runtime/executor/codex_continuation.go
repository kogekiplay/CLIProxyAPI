package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	defaultCodexContinuationMaxContinue    = 3
	defaultCodexContinuationMinN           = 1
	defaultCodexContinuationMaxN           = 6
	defaultCodexContinuationTruncationStep = 518
	defaultCodexContinuationMarkerText     = "Continue thinking..."
)

type codexContinuationOptions struct {
	Enabled               bool
	MaxContinue           int
	MinN                  int
	MaxN                  int
	TruncationStep        int
	MarkerText            string
	ForwardMarker         bool
	MaxTotalOutputTokens  int64
	ForceIncludeEncrypted bool
}

type codexContinuationResult struct {
	Rounds    int
	Continued bool
}

type codexContinuationOpenRound func(context.Context, []byte) (*http.Response, error)

type codexContinuationUsage struct {
	InputTokens     int64
	OutputTokens    int64
	TotalTokens     int64
	CachedTokens    *int64
	ReasoningTokens *int64
}

type codexContinuationRoundInfo struct {
	Round           int    `json:"round"`
	ReasoningTokens *int64 `json:"reasoning_tokens,omitempty"`
	N               *int64 `json:"n,omitempty"`
}

type codexContinuationBufferedItem struct {
	UpstreamOutputIndex string
	ItemType            string
	Events              [][]byte
	Item                []byte
}

type codexContinuationRoundState struct {
	Terminal       []byte
	Usage          *codexContinuationUsage
	SawDone        bool
	RoundReasoning [][]byte
	OutBuffer      []codexContinuationBufferedItem
	SawTerminal    bool
}

type codexContinuationFoldState struct {
	Options       codexContinuationOptions
	BaseBody      []byte
	OriginalInput [][]byte
	ReplayTail    [][]byte
	Sequence      int64
	OutputIndex   int64
	BaseResponse  []byte
	FinalOutput   [][]byte
	TotalUsage    codexContinuationUsage
	FirstUsage    *codexContinuationUsage
	RoundsInfo    []codexContinuationRoundInfo
}

func codexContinuationOptionsFromConfig(cfg *config.Config) codexContinuationOptions {
	cont := config.CodexContinuationConfig{}
	if cfg != nil {
		cont = cfg.Codex.Continuation
	}
	enabled := true
	if cont.Enabled != nil {
		enabled = *cont.Enabled
	}
	forceIncludeEncrypted := true
	if cont.ForceIncludeEncrypted != nil {
		forceIncludeEncrypted = *cont.ForceIncludeEncrypted
	}
	opts := codexContinuationOptions{
		Enabled:               enabled,
		MaxContinue:           cont.MaxContinue,
		MinN:                  cont.MinN,
		MaxN:                  cont.MaxN,
		TruncationStep:        cont.TruncationStep,
		MarkerText:            strings.TrimSpace(cont.MarkerText),
		ForwardMarker:         cont.ForwardMarker,
		MaxTotalOutputTokens:  cont.MaxTotalOutputTokens,
		ForceIncludeEncrypted: forceIncludeEncrypted,
	}
	if opts.MaxContinue <= 0 {
		opts.MaxContinue = defaultCodexContinuationMaxContinue
	}
	if opts.MinN <= 0 {
		opts.MinN = defaultCodexContinuationMinN
	}
	if opts.MaxN < 0 {
		opts.MaxN = 0
	} else if opts.MaxN == 0 {
		opts.MaxN = defaultCodexContinuationMaxN
	}
	if opts.TruncationStep <= 0 {
		opts.TruncationStep = defaultCodexContinuationTruncationStep
	}
	if opts.MarkerText == "" {
		opts.MarkerText = defaultCodexContinuationMarkerText
	}
	return opts
}

func codexContinuationEnabledForBody(cfg *config.Config, body []byte) bool {
	opts := codexContinuationOptionsFromConfig(cfg)
	if !opts.Enabled || len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	if gjson.GetBytes(body, "stream").Type == gjson.False {
		return false
	}
	return gjson.GetBytes(body, "reasoning").Type != gjson.False
}

func codexContinuationPrepareBody(cfg *config.Config, body []byte) []byte {
	opts := codexContinuationOptionsFromConfig(cfg)
	if !opts.Enabled || !opts.ForceIncludeEncrypted {
		return body
	}
	return codexContinuationEnsureEncryptedInclude(body)
}

func foldCodexContinuationStream(ctx context.Context, opts codexContinuationOptions, baseBody []byte, first *http.Response, openRound codexContinuationOpenRound, emit func([]byte)) (codexContinuationResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if first == nil || first.Body == nil {
		return codexContinuationResult{}, fmt.Errorf("codex continuation: first response body is nil")
	}
	if emit == nil {
		emit = func([]byte) {}
	}
	state := &codexContinuationFoldState{
		Options:       opts,
		BaseBody:      append([]byte(nil), baseBody...),
		OriginalInput: codexContinuationInputItems(baseBody),
	}
	response := first
	roundNo := 0
	continued := false

	for {
		roundNo++
		roundState, err := state.readRound(response.Body, roundNo, emit)
		if closeErr := response.Body.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		if err != nil {
			state.emitSyntheticIncomplete(emit, "upstream_error")
			return codexContinuationResult{Rounds: roundNo, Continued: continued}, nil
		}
		state.addUsage(roundState.Usage)
		if roundNo == 1 {
			state.FirstUsage = roundState.Usage
		}
		state.appendRoundInfo(roundNo, roundState.Usage)

		doContinue := state.shouldContinue(roundNo, roundState)
		stoppedReason := state.stoppedReason(roundNo, roundState, doContinue)
		if doContinue {
			continued = true
			state.appendReplayTail(roundState.RoundReasoning)
			payload := state.nextPayload()
			if openRound == nil {
				state.emitSyntheticIncomplete(emit, "upstream_error")
				return codexContinuationResult{Rounds: roundNo, Continued: continued}, nil
			}
			next, errOpen := openRound(ctx, payload)
			if errOpen != nil || next == nil || next.Body == nil {
				state.emitSyntheticIncomplete(emit, "upstream_error")
				return codexContinuationResult{Rounds: roundNo, Continued: continued}, nil
			}
			if next.StatusCode < http.StatusOK || next.StatusCode >= http.StatusMultipleChoices {
				if next.Body != nil {
					_, _ = io.Copy(io.Discard, io.LimitReader(next.Body, 4096))
					_ = next.Body.Close()
				}
				state.emitSyntheticIncomplete(emit, "upstream_error")
				return codexContinuationResult{Rounds: roundNo, Continued: continued}, nil
			}
			response = next
			continue
		}

		if !roundState.SawTerminal {
			state.emitSyntheticIncomplete(emit, "upstream_eof")
			return codexContinuationResult{Rounds: roundNo, Continued: continued}, nil
		}
		state.flushBufferedOutput(roundState.OutBuffer, emit)
		state.emitTerminal(roundState.Terminal, roundState.Usage, true, stoppedReason, emit)
		if roundState.SawDone {
			emit([]byte("data: [DONE]"))
		}
		return codexContinuationResult{Rounds: roundNo, Continued: continued}, nil
	}
}

func (s *codexContinuationFoldState) readRound(body io.Reader, roundNo int, emit func([]byte)) (codexContinuationRoundState, error) {
	var state codexContinuationRoundState
	itemKind := make(map[string]string)
	outputIndexMap := make(map[string]int64)
	var dataLines []string
	scanner := bufio.NewScanner(body)
	scanner.Buffer(nil, 52_428_800)

	flushEvent := func() {
		if len(dataLines) == 0 {
			dataLines = nil
			return
		}
		payload := strings.Join(dataLines, "\n")
		dataLines = nil
		if payload == "[DONE]" {
			state.SawDone = true
			return
		}
		if state.SawTerminal {
			return
		}
		if !gjson.Valid(payload) {
			return
		}
		s.processEvent(&state, []byte(payload), roundNo, itemKind, outputIndexMap, emit)
	}

	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			flushEvent()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			value := line[len("data:"):]
			if strings.HasPrefix(value, " ") {
				value = value[1:]
			}
			dataLines = append(dataLines, value)
			if len(dataLines) == 1 && (value == "[DONE]" || gjson.Valid(value)) {
				flushEvent()
			}
		}
	}
	flushEvent()
	if err := scanner.Err(); err != nil {
		return state, err
	}
	return state, nil
}

func (s *codexContinuationFoldState) processEvent(roundState *codexContinuationRoundState, event []byte, roundNo int, itemKind map[string]string, outputIndexMap map[string]int64, emit func([]byte)) {
	eventType := gjson.GetBytes(event, "type").String()
	switch eventType {
	case "response.created", "response.in_progress":
		if roundNo == 1 {
			if eventType == "response.created" {
				if response := gjson.GetBytes(event, "response"); response.Exists() && response.IsObject() {
					s.BaseResponse = []byte(response.Raw)
				}
			}
			emit(s.withSequence(event))
		}
		return
	case "response.completed", "response.failed", "response.incomplete":
		roundState.Terminal = append([]byte(nil), event...)
		roundState.Usage = codexContinuationParseUsage(gjson.GetBytes(event, "response.usage"))
		roundState.SawTerminal = true
		return
	}

	upstreamIndex := codexContinuationOutputIndexKey(event)
	if eventType == "response.output_item.added" {
		item := gjson.GetBytes(event, "item")
		itemType := strings.TrimSpace(item.Get("type").String())
		if itemType == "reasoning" {
			itemKind[upstreamIndex] = "reasoning"
			outputIndexMap[upstreamIndex] = s.OutputIndex
			s.OutputIndex++
			event = codexContinuationSetInt(event, "output_index", outputIndexMap[upstreamIndex])
			emit(s.withSequence(event))
			return
		}
		itemKind[upstreamIndex] = "buffered"
		scratch := codexContinuationBufferedItem{
			UpstreamOutputIndex: upstreamIndex,
			ItemType:            itemType,
			Events:              [][]byte{append([]byte(nil), event...)},
		}
		if item.Exists() && item.IsObject() {
			scratch.Item = []byte(item.Raw)
		}
		roundState.OutBuffer = append(roundState.OutBuffer, scratch)
		return
	}

	switch itemKind[upstreamIndex] {
	case "reasoning":
		if mapped, ok := outputIndexMap[upstreamIndex]; ok {
			event = codexContinuationSetInt(event, "output_index", mapped)
		}
		if eventType == "response.output_item.done" {
			if item := gjson.GetBytes(event, "item"); item.Exists() && item.IsObject() {
				roundState.RoundReasoning = append(roundState.RoundReasoning, []byte(item.Raw))
				s.FinalOutput = append(s.FinalOutput, []byte(item.Raw))
			}
		}
		emit(s.withSequence(event))
	case "buffered":
		if entry := codexContinuationFindBuffer(roundState.OutBuffer, upstreamIndex); entry != nil {
			entry.Events = append(entry.Events, append([]byte(nil), event...))
			if eventType == "response.output_item.done" {
				if item := gjson.GetBytes(event, "item"); item.Exists() && item.IsObject() {
					entry.Item = []byte(item.Raw)
				}
			}
		}
	default:
		emit(s.withSequence(event))
	}
}

func (s *codexContinuationFoldState) withSequence(event []byte) []byte {
	updated := codexContinuationSetInt(event, "sequence_number", s.Sequence)
	s.Sequence++
	return codexContinuationDataLine(updated)
}

func (s *codexContinuationFoldState) flushBufferedOutput(items []codexContinuationBufferedItem, emit func([]byte)) {
	for _, item := range items {
		for _, event := range item.Events {
			if gjson.GetBytes(event, "output_index").Exists() {
				event = codexContinuationSetInt(event, "output_index", s.OutputIndex)
			}
			emit(s.withSequence(event))
		}
		s.OutputIndex++
		if len(item.Item) > 0 {
			s.FinalOutput = append(s.FinalOutput, append([]byte(nil), item.Item...))
		}
	}
}

func (s *codexContinuationFoldState) shouldContinue(roundNo int, state codexContinuationRoundState) bool {
	if !s.Options.Enabled || !state.SawTerminal || state.Usage == nil || state.Usage.ReasoningTokens == nil {
		return false
	}
	if !codexContinuationShouldContinue(*state.Usage.ReasoningTokens, s.Options.MinN, s.Options.MaxN, s.Options.TruncationStep) {
		return false
	}
	if len(state.RoundReasoning) == 0 || !gjson.GetBytes(state.RoundReasoning[len(state.RoundReasoning)-1], "encrypted_content").Exists() {
		return false
	}
	if roundNo > s.Options.MaxContinue {
		return false
	}
	if s.Options.MaxTotalOutputTokens > 0 && s.TotalUsage.OutputTokens >= s.Options.MaxTotalOutputTokens {
		return false
	}
	return true
}

func (s *codexContinuationFoldState) stoppedReason(roundNo int, state codexContinuationRoundState, willContinue bool) string {
	if willContinue || state.Usage == nil || state.Usage.ReasoningTokens == nil {
		return ""
	}
	reasoningTokens := *state.Usage.ReasoningTokens
	if _, ok := codexContinuationTier(reasoningTokens, s.Options.TruncationStep); !ok {
		return ""
	}
	if len(state.RoundReasoning) == 0 || !gjson.GetBytes(state.RoundReasoning[len(state.RoundReasoning)-1], "encrypted_content").Exists() {
		return "no_encrypted_content"
	}
	if roundNo > s.Options.MaxContinue {
		return "max_continue"
	}
	if s.Options.MaxTotalOutputTokens > 0 && s.TotalUsage.OutputTokens >= s.Options.MaxTotalOutputTokens {
		return "max_total_output_tokens"
	}
	return "tier_out_of_window"
}

func (s *codexContinuationFoldState) appendReplayTail(reasoningItems [][]byte) {
	for _, item := range reasoningItems {
		s.ReplayTail = append(s.ReplayTail, append([]byte(nil), item...))
	}
	s.ReplayTail = append(s.ReplayTail, codexContinuationCommentaryMessage(s.Options.MarkerText))
}

func (s *codexContinuationFoldState) nextPayload() []byte {
	items := make([][]byte, 0, len(s.OriginalInput)+len(s.ReplayTail))
	items = append(items, s.OriginalInput...)
	items = append(items, s.ReplayTail...)
	payload := append([]byte(nil), s.BaseBody...)
	payload, _ = sjson.SetRawBytes(payload, "input", codexContinuationJSONArray(items))
	payload, _ = sjson.SetBytes(payload, "stream", true)
	payload, _ = sjson.DeleteBytes(payload, "previous_response_id")
	if s.Options.ForceIncludeEncrypted {
		payload = codexContinuationEnsureEncryptedInclude(payload)
	}
	return payload
}

func (s *codexContinuationFoldState) emitTerminal(terminal []byte, finalRoundUsage *codexContinuationUsage, flushedFinal bool, stoppedReason string, emit func([]byte)) {
	response := gjson.GetBytes(terminal, "response")
	responseRaw := []byte(response.Raw)
	if len(responseRaw) == 0 || !response.IsObject() {
		responseRaw = append([]byte(nil), s.BaseResponse...)
	}
	if len(responseRaw) == 0 {
		responseRaw = []byte(`{}`)
	}
	responseRaw, _ = sjson.SetRawBytes(responseRaw, "output", codexContinuationJSONArray(s.FinalOutput))
	responseRaw, _ = sjson.SetRawBytes(responseRaw, "usage", s.agentUsageJSON(finalRoundUsage, flushedFinal))
	if status := strings.TrimSpace(response.Get("status").String()); status != "" {
		responseRaw, _ = sjson.SetBytes(responseRaw, "status", status)
	}
	if details := response.Get("incomplete_details"); details.Exists() {
		responseRaw, _ = sjson.SetRawBytes(responseRaw, "incomplete_details", []byte(details.Raw))
	}
	responseRaw = s.withProxyMetadata(responseRaw, stoppedReason)

	event := append([]byte(nil), terminal...)
	if len(event) == 0 || !gjson.ValidBytes(event) {
		event = []byte(`{"type":"response.completed"}`)
	}
	event, _ = sjson.SetRawBytes(event, "response", responseRaw)
	emit(s.withSequence(event))
}

func (s *codexContinuationFoldState) emitSyntheticIncomplete(emit func([]byte), reason string) {
	responseRaw := append([]byte(nil), s.BaseResponse...)
	if len(responseRaw) == 0 {
		responseRaw = []byte(`{}`)
	}
	responseRaw, _ = sjson.SetRawBytes(responseRaw, "output", codexContinuationJSONArray(s.FinalOutput))
	responseRaw, _ = sjson.SetRawBytes(responseRaw, "usage", s.agentUsageJSON(nil, false))
	responseRaw, _ = sjson.SetBytes(responseRaw, "status", "incomplete")
	responseRaw, _ = sjson.SetRawBytes(responseRaw, "incomplete_details", []byte(fmt.Sprintf(`{"reason":%q}`, reason)))
	responseRaw = s.withProxyMetadata(responseRaw, reason)
	event := []byte(`{"type":"response.incomplete"}`)
	event, _ = sjson.SetRawBytes(event, "response", responseRaw)
	emit(s.withSequence(event))
}

func (s *codexContinuationFoldState) withProxyMetadata(response []byte, stoppedReason string) []byte {
	if len(s.RoundsInfo) <= 1 && stoppedReason == "" {
		return response
	}
	rounds, _ := json.Marshal(s.RoundsInfo)
	response, _ = sjson.SetRawBytes(response, "metadata.proxy_rounds", rounds)
	response, _ = sjson.SetRawBytes(response, "metadata.proxy_billed_usage", s.TotalUsage.JSON())
	if stoppedReason != "" {
		response, _ = sjson.SetBytes(response, "metadata.proxy_stopped_reason", stoppedReason)
	}
	return response
}

func (s *codexContinuationFoldState) agentUsageJSON(finalRoundUsage *codexContinuationUsage, flushedFinal bool) []byte {
	inputTokens := int64(0)
	var cached *int64
	if s.FirstUsage != nil {
		inputTokens = s.FirstUsage.InputTokens
		cached = s.FirstUsage.CachedTokens
	}
	reasoning := int64(0)
	if s.TotalUsage.ReasoningTokens != nil {
		reasoning = *s.TotalUsage.ReasoningTokens
	}
	finalNonReasoning := int64(0)
	if flushedFinal && finalRoundUsage != nil {
		finalReasoning := int64(0)
		if finalRoundUsage.ReasoningTokens != nil {
			finalReasoning = *finalRoundUsage.ReasoningTokens
		}
		finalNonReasoning = finalRoundUsage.OutputTokens - finalReasoning
		if finalNonReasoning < 0 {
			finalNonReasoning = 0
		}
	}
	usage := codexContinuationUsage{
		InputTokens:     inputTokens,
		OutputTokens:    reasoning + finalNonReasoning,
		TotalTokens:     inputTokens + reasoning + finalNonReasoning,
		CachedTokens:    cached,
		ReasoningTokens: &reasoning,
	}
	return usage.JSON()
}

func (s *codexContinuationFoldState) addUsage(usage *codexContinuationUsage) {
	if usage == nil {
		return
	}
	s.TotalUsage.InputTokens += usage.InputTokens
	s.TotalUsage.OutputTokens += usage.OutputTokens
	s.TotalUsage.TotalTokens += usage.TotalTokens
	if usage.CachedTokens != nil {
		if s.TotalUsage.CachedTokens == nil {
			v := int64(0)
			s.TotalUsage.CachedTokens = &v
		}
		*s.TotalUsage.CachedTokens += *usage.CachedTokens
	}
	if usage.ReasoningTokens != nil {
		if s.TotalUsage.ReasoningTokens == nil {
			v := int64(0)
			s.TotalUsage.ReasoningTokens = &v
		}
		*s.TotalUsage.ReasoningTokens += *usage.ReasoningTokens
	}
}

func (s *codexContinuationFoldState) appendRoundInfo(roundNo int, usage *codexContinuationUsage) {
	info := codexContinuationRoundInfo{Round: roundNo}
	if usage != nil && usage.ReasoningTokens != nil {
		rt := *usage.ReasoningTokens
		info.ReasoningTokens = &rt
		if n, ok := codexContinuationTier(rt, s.Options.TruncationStep); ok {
			info.N = &n
		}
	}
	s.RoundsInfo = append(s.RoundsInfo, info)
}

func codexContinuationFindBuffer(items []codexContinuationBufferedItem, upstreamIndex string) *codexContinuationBufferedItem {
	for i := range items {
		if items[i].UpstreamOutputIndex == upstreamIndex {
			return &items[i]
		}
	}
	return nil
}

func codexContinuationOutputIndexKey(event []byte) string {
	if outputIndex := gjson.GetBytes(event, "output_index"); outputIndex.Exists() {
		return outputIndex.Raw
	}
	return "__missing__"
}

func codexContinuationSetInt(event []byte, path string, value int64) []byte {
	updated, err := sjson.SetBytes(event, path, value)
	if err != nil {
		return event
	}
	return updated
}

func codexContinuationDataLine(event []byte) []byte {
	return append([]byte("data: "), event...)
}

func codexContinuationParseUsage(usage gjson.Result) *codexContinuationUsage {
	if !usage.Exists() || !usage.IsObject() {
		return nil
	}
	out := &codexContinuationUsage{
		InputTokens:  usage.Get("input_tokens").Int(),
		OutputTokens: usage.Get("output_tokens").Int(),
		TotalTokens:  usage.Get("total_tokens").Int(),
	}
	if cached := usage.Get("input_tokens_details.cached_tokens"); cached.Exists() {
		v := cached.Int()
		out.CachedTokens = &v
	}
	if reasoning := usage.Get("output_tokens_details.reasoning_tokens"); reasoning.Exists() {
		v := reasoning.Int()
		out.ReasoningTokens = &v
	}
	return out
}

func codexContinuationBilledUsageDetail(eventData []byte) (usage.Detail, bool) {
	node := gjson.GetBytes(eventData, "response.metadata.proxy_billed_usage")
	if !node.Exists() || !node.IsObject() {
		return usage.Detail{}, false
	}
	detail := usage.Detail{
		InputTokens:  node.Get("input_tokens").Int(),
		OutputTokens: node.Get("output_tokens").Int(),
		TotalTokens:  node.Get("total_tokens").Int(),
	}
	if cached := node.Get("input_tokens_details.cached_tokens"); cached.Exists() {
		detail.CachedTokens = cached.Int()
		detail.CacheReadTokens = cached.Int()
	}
	if reasoning := node.Get("output_tokens_details.reasoning_tokens"); reasoning.Exists() {
		detail.ReasoningTokens = reasoning.Int()
	}
	return detail, true
}

func (u codexContinuationUsage) JSON() []byte {
	payload := map[string]any{
		"input_tokens":  u.InputTokens,
		"output_tokens": u.OutputTokens,
		"total_tokens":  u.TotalTokens,
	}
	if u.CachedTokens != nil {
		payload["input_tokens_details"] = map[string]any{"cached_tokens": *u.CachedTokens}
	}
	if u.ReasoningTokens != nil {
		payload["output_tokens_details"] = map[string]any{"reasoning_tokens": *u.ReasoningTokens}
	}
	out, _ := json.Marshal(payload)
	return out
}

func codexContinuationShouldContinue(reasoningTokens int64, minN, maxN, step int) bool {
	n, ok := codexContinuationTier(reasoningTokens, step)
	if !ok {
		return false
	}
	if n < int64(minN) {
		return false
	}
	return maxN == 0 || n <= int64(maxN)
}

func codexContinuationTier(reasoningTokens int64, step int) (int64, bool) {
	if step <= 0 {
		step = defaultCodexContinuationTruncationStep
	}
	if reasoningTokens < int64(step-2) || (reasoningTokens+2)%int64(step) != 0 {
		return 0, false
	}
	return (reasoningTokens + 2) / int64(step), true
}

func codexContinuationInputItems(body []byte) [][]byte {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return nil
	}
	items := input.Array()
	out := make([][]byte, 0, len(items))
	for _, item := range items {
		out = append(out, []byte(item.Raw))
	}
	return out
}

func codexContinuationCommentaryMessage(text string) []byte {
	payload := map[string]any{
		"type": "message",
		"role": "assistant",
		"content": []map[string]string{{
			"type": "output_text",
			"text": text,
		}},
		"phase": "commentary",
	}
	out, _ := json.Marshal(payload)
	return out
}

func codexContinuationJSONArray(items [][]byte) []byte {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		if len(item) == 0 || !gjson.ValidBytes(item) {
			b.WriteString("null")
			continue
		}
		b.Write(item)
	}
	b.WriteByte(']')
	return b.Bytes()
}

func codexContinuationEnsureEncryptedInclude(body []byte) []byte {
	if codexContinuationIncludeHasEncrypted(body) {
		return body
	}
	include := gjson.GetBytes(body, "include")
	items := make([][]byte, 0)
	if include.IsArray() {
		for _, item := range include.Array() {
			if item.Type == gjson.String {
				encoded, _ := json.Marshal(item.String())
				items = append(items, encoded)
			}
		}
	}
	items = append(items, []byte(`"reasoning.encrypted_content"`))
	updated, err := sjson.SetRawBytes(body, "include", codexContinuationJSONArray(items))
	if err != nil {
		return body
	}
	return updated
}

func codexContinuationIncludeHasEncrypted(body []byte) bool {
	include := gjson.GetBytes(body, "include")
	if !include.IsArray() {
		return false
	}
	for _, item := range include.Array() {
		if item.String() == "reasoning.encrypted_content" {
			return true
		}
	}
	return false
}
