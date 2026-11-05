package chatstream

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/sse"
)

// ServeChatStream — общий релей chat/completions-SSE → Anthropic-SSE
// (openai и copilot трансляторы). Семантика:
//   - writer ленивый: до первого события заголовки не пишутся (ошибка
//     пустого стрима отдаётся как 502 JSON, а не пустой SSE);
//   - [DONE] обязателен: EOF без него = обрыв апстрима → SSE-error + финал
//     без маскировки под нормальное завершение;
//   - message_stop строго {"type":"message_stop"}.
//
//nolint:gocognit // релей: ленивый writer + обработка обрыва, разбиение затемнит
func ServeChatStream(w http.ResponseWriter, resp *http.Response, model string) {
	reader := sse.NewReader(resp.Body)
	conv := NewStreamConverter(model)

	var writer *sse.Writer
	emit := func(ev models.AnthropicEvent) {
		if writer == nil {
			writer = sse.NewWriter(w)
		}
		writer.Event(ev.Type, ev)
	}

	for {
		ev, err := reader.Next()
		if err != nil {
			// обрыв до [DONE]: не тихий end_turn — явная ошибка клиенту
			if writer != nil {
				emit(models.AnthropicEvent{
					Type:  "error",
					Error: &models.AnthropicError{Type: "api_error", Message: "upstream stream interrupted"},
				})
				for _, out := range conv.FinishBroken() {
					emit(out)
				}
				writer.Close()
			} else {
				writeStreamError(w, http.StatusBadGateway, "upstream stream interrupted before any event")
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
}

func writeStreamError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":%q}}`, msg) //nolint:gosec // %q экранирует
}
