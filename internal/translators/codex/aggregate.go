package codex

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/usagelog"
	"github.com/ivangurin/ccx/internal/translators/relay"
)

// serveAggregate answers a NON-streaming client request from a streaming
// upstream (the backend contract is stream-only): the Responses SSE is
// converted to Anthropic events and folded into one MessagesResponse.
func (t *Translator) serveAggregate(w http.ResponseWriter, resp *http.Response, model string, entry *usagelog.Entry) {
	var events []models.AnthropicEvent
	started := t.streamEvents(resp, model, func(ev models.AnthropicEvent) {
		events = append(events, ev)
	})
	if !started {
		entry.Status, entry.Error = http.StatusBadGateway, "codex: empty upstream stream"
		relay.WriteErr(w, http.StatusBadGateway, "codex: empty upstream stream")

		return
	}
	out, errMsg := eventsToMessage(events, model)
	if errMsg != "" {
		// a failed stream must not masquerade as a complete message
		entry.Status, entry.Error = http.StatusBadGateway, "codex: "+errMsg
		relay.WriteErr(w, http.StatusBadGateway, "codex: "+errMsg)

		return
	}
	entry.InputTokens, entry.OutputTokens = out.Usage.InputTokens, out.Usage.OutputTokens
	data, err := json.Marshal(out) //nolint:errchkjson // assembled from typed events
	if err != nil {
		relay.WriteErr(w, http.StatusInternalServerError, err.Error())

		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// eventsToMessage folds one turn's Anthropic events into a MessagesResponse.
// The second return is a non-empty error message when the sequence carries an
// error event — the caller answers 502, a partial message would look complete.
func eventsToMessage(events []models.AnthropicEvent, model string) (*models.MessagesResponse, string) {
	for _, ev := range events {
		if ev.Type == evError {
			return nil, errorMessage(ev)
		}
	}
	f := newFolder(model)
	for _, ev := range events {
		f.apply(ev)
	}
	f.finish()

	return f.out, ""
}

func errorMessage(ev models.AnthropicEvent) string {
	if ev.Error != nil && ev.Error.Message != "" {
		return ev.Error.Message
	}

	return "response failed"
}

// folder accumulates content_block events into the final response; tool_use
// arguments arrive as partial-JSON fragments, so they buffer per block and
// assemble at finish.
type folder struct {
	out       *models.MessagesResponse
	inputJSON []string
}

func newFolder(model string) *folder {
	return &folder{out: &models.MessagesResponse{
		ID: "resp", Type: typeMessage, Role: roleAssistantS, Model: model,
		StopReason: strPtr("end_turn"),
	}}
}

func (f *folder) apply(ev models.AnthropicEvent) {
	switch ev.Type {
	case "content_block_start":
		f.startBlock(ev)
	case "content_block_delta":
		f.applyDelta(ev)
	case evMessageDelta:
		f.applyMessageDelta(ev)
	default:
		// message_start/stop, content_block_stop — nothing to fold
	}
}

func (f *folder) startBlock(ev models.AnthropicEvent) {
	if ev.ContentBlock == nil || ev.Index == nil {
		return
	}
	blk := blockFromEvent(*ev.ContentBlock)
	for len(f.out.Content) <= *ev.Index {
		f.out.Content = append(f.out.Content, models.ContentBlock{})
		f.inputJSON = append(f.inputJSON, "")
	}
	f.out.Content[*ev.Index] = blk
	f.inputJSON[*ev.Index] = ""
}

func (f *folder) applyDelta(ev models.AnthropicEvent) {
	if ev.Delta == nil || ev.Index == nil || *ev.Index >= len(f.out.Content) {
		return
	}
	blk := &f.out.Content[*ev.Index]
	switch ev.Delta.Type {
	case "text_delta":
		blk.Text = strPtr(derefStr(blk.Text) + ev.Delta.Text)
	case "thinking_delta":
		blk.Thinking = strPtr(derefStr(blk.Thinking) + ev.Delta.Thinking)
	case "input_json_delta":
		f.inputJSON[*ev.Index] += ev.Delta.PartialJSON
	default:
		// signature and unknown deltas carry nothing foldable
	}
}

func (f *folder) applyMessageDelta(ev models.AnthropicEvent) {
	if ev.Delta != nil && ev.Delta.StopReason != "" {
		f.out.StopReason = strPtr(ev.Delta.StopReason)
	}
	if ev.Usage != nil {
		f.out.Usage = *ev.Usage
	}
}

// finish assembles the buffered tool arguments; empty fragments mean no
// arguments — an explicit {} (not a nil input).
func (f *folder) finish() {
	for i, blk := range f.out.Content {
		if blk.Type != typeToolUse {
			continue
		}
		if i < len(f.inputJSON) && strings.TrimSpace(f.inputJSON[i]) != "" {
			f.out.Content[i].Input = json.RawMessage(f.inputJSON[i])
		} else {
			f.out.Content[i].Input = json.RawMessage("{}")
		}
	}
}

func strPtr(s string) *string { return &s }

func derefStr(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

// blockFromEvent converts an SSE content block (value strings) into the
// response DTO (pointer strings) — two types, one shape.
func blockFromEvent(src models.AnthropicBlock) models.ContentBlock {
	return models.ContentBlock{
		Type:      src.Type,
		Text:      ptrIf(src.Text),
		Thinking:  ptrIf(src.Thinking),
		ID:        ptrIf(src.ID),
		Name:      ptrIf(src.Name),
		Input:     src.Input,
		ToolUseID: ptrIf(src.ToolUseID),
		Content:   src.Content,
	}
}

func ptrIf(s string) *string {
	if s == "" {
		return nil
	}

	return &s
}
