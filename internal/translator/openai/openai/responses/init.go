package responses

import (
	"sync"

	. "github.com/router-for-me/CLIProxyAPI/v7/internal/constant"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/translator/translator"
)

// ThinkTagParsingMode controls how <think> tags in OpenAI-compatible responses are handled.
// Valid values: "auto" (default, parse tags when detected), "on" (always parse), "off" (never parse).
var (
	thinkTagParsingMode   = "auto"
	thinkTagParsingModeMu sync.RWMutex
)

// SetThinkTagParsingMode sets the global think tag parsing mode.
// Valid values: "auto", "on", "off".
func SetThinkTagParsingMode(mode string) {
	thinkTagParsingModeMu.Lock()
	defer thinkTagParsingModeMu.Unlock()
	switch mode {
	case "auto", "on", "off":
		thinkTagParsingMode = mode
	default:
		thinkTagParsingMode = "auto"
	}
}

// GetThinkTagParsingMode returns the current think tag parsing mode.
func GetThinkTagParsingMode() string {
	thinkTagParsingModeMu.RLock()
	defer thinkTagParsingModeMu.RUnlock()
	return thinkTagParsingMode
}

// shouldParseThinkTags returns true if think tag parsing should be applied.
// In "auto" mode, parsing is enabled (tags are only processed when present).
// In "on" mode, parsing is always enabled.
// In "off" mode, parsing is disabled.
func shouldParseThinkTags() bool {
	mode := GetThinkTagParsingMode()
	return mode == "auto" || mode == "on"
}

func init() {
	translator.Register(
		OpenaiResponse,
		OpenAI,
		ConvertOpenAIResponsesRequestToOpenAIChatCompletions,
		interfaces.TranslateResponse{
			Stream:    ConvertOpenAIChatCompletionsResponseToOpenAIResponses,
			NonStream: ConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream,
		},
	)
}
