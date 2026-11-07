package chatstream

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/sse"
)

// ServeChatStream is the shared chat/completions-SSE → Anthropic-SSE relay
// (openai and copilot translators). Semantics:
//   - the writer is lazy: headers are not written before the first event
//     (an empty-stream error is returned as 502 JSON, not an empty SSE);
//   - [DONE] is mandatory: EOF without it = upstream break → SSE error + finale
//     without masquerading as a normal completion;
//   - message_stop is strictly {"type":"message_stop"};
//   - onFinish (nil-safe) fires only on the clean path with the final usage
//     and the time to first token — the two numbers only the relay sees;
//   - onBreak (nil-safe) fires when the stream breaks instead — the usage
//     entry must not record a clean 200 for a generation the client never
//     finished receiving.
//
//nolint:gocognit // relay: lazy writer + break handling, splitting would obscure
func ServeChatStream(
	w http.ResponseWriter,
	resp *http.Response,
	model string,
	onFinish func(ttft time.Duration, usage models.Usage),
	onBreak func(msg string),
) {
	reader := sse.NewReader(resp.Body)
	conv := NewStreamConverter(model)
	started := time.Now()
	var ttft time.Duration

	var writer *sse.Writer
	emit := func(ev models.AnthropicEvent) {
		if writer == nil {
			writer = sse.NewWriter(w)
		}
		if ttft == 0 {
			ttft = time.Since(started)
		}
		writer.Event(ev.Type, ev)
	}

	for {
		ev, err := reader.Next()
		if err != nil {
			// break before [DONE]: not a silent end_turn — an explicit error to the client
			if writer != nil {
				emit(models.AnthropicEvent{
					Type:  "error",
					Error: &models.AnthropicError{Type: "api_error", Message: "upstream stream interrupted"},
				})
				for _, out := range conv.FinishBroken() {
					emit(out)
				}
				writer.Close()
				if onBreak != nil {
					onBreak("upstream stream interrupted")
				}
			} else {
				writeStreamError(w, http.StatusBadGateway, "upstream stream interrupted before any event")
				if onBreak != nil {
					onBreak("upstream stream interrupted before any event")
				}
			}

			return
		}
		if ev.Done {
			break
		}
		var chunk models.ChatChunk
		if jsonErr := json.Unmarshal([]byte(ev.Data), &chunk); jsonErr != nil {
			continue
		}
		for _, out := range conv.Feed(&chunk) {
			emit(out)
		}
	}
	if writer == nil {
		writeStreamError(w, http.StatusBadGateway, "upstream returned empty stream")

		return
	}
	for _, out := range conv.Finish() {
		emit(out)
	}
	writer.Close()
	if onFinish != nil {
		onFinish(ttft, conv.finalUsage())
	}
}

func writeStreamError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":%q}}`, msg) //nolint:gosec // %q escapes
}
