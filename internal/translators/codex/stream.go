package codex

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/sse"
	"github.com/ivangurin/ccx/internal/translators/relay"
)

// streamEvent is a typed Responses SSE event.
type streamEvent struct {
	Type   string `json:"type"`
	Delta  string `json:"delta"`
	ItemID string `json:"item_id"`
	Item   *struct {
		Type   string `json:"type"`
		Name   string `json:"name"`
		CallID string `json:"call_id"`
		ID     string `json:"id"`
		Action *struct {
			Type  string `json:"type"`
			Query string `json:"query"`
		} `json:"action"`
	} `json:"item"`
	Annotation *struct {
		Type  string `json:"type"`
		URL   string `json:"url"`
		Title string `json:"title"`
	} `json:"annotation"`
	Response *struct {
		Usage *models.Usage `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
}

// serveStream relays the upstream Responses SSE to the client as Anthropic
// SSE (lazy writer: an empty stream is a 502 JSON, not an empty SSE).
func (t *Translator) serveStream(w http.ResponseWriter, resp *http.Response, model string) {
	var writer *sse.Writer
	started := t.streamEvents(resp, model, func(ev models.AnthropicEvent) {
		if writer == nil {
			writer = sse.NewWriter(w)
		}
		writer.Event(ev.Type, ev)
	})
	if !started {
		relay.WriteErr(w, http.StatusBadGateway, "codex: empty upstream stream")

		return
	}
	writer.Close()
}

// streamEvents converts the upstream Responses SSE into Anthropic events via
// emit. Returns false when the stream carried nothing at all (the caller
// answers 502 — a plain JSON error, not an empty SSE).
//
//nolint:gocognit,gocyclo // linear dispatcher over Responses SSE event types
func (t *Translator) streamEvents(resp *http.Response, model string, emit func(models.AnthropicEvent)) bool {
	reader := sse.NewReader(resp.Body)
	nextIndex := 0
	itemIdx := map[string]int{}
	itemIsTool := map[string]bool{}
	itemOrder := []string{}
	var usage models.Usage
	stopReason := "end_turn"
	var search webSearchCollector
	started := false

	openBlock := func(itemID string, block models.AnthropicBlock) {
		idx := nextIndex
		nextIndex++
		itemIdx[itemID] = idx
		itemIsTool[itemID] = block.Type == typeToolUse
		itemOrder = append(itemOrder, itemID)
		emit(models.AnthropicEvent{Type: "content_block_start", Index: &idx, ContentBlock: &block})
	}

	for {
		ev, err := reader.Next()
		if err != nil {
			// The Responses stream ends with the connection close — there is
			// no [DONE] marker (unlike chat/completions), so a clean io.EOF
			// ends the loop like Done; only a real error is an interruption.
			if errors.Is(err, io.EOF) {
				break
			}
			// break/empty stream before a single event — a plain 502 JSON response
			if !started {
				return false
			}
			emit(models.AnthropicEvent{
				Type:  evError,
				Error: &models.AnthropicError{Type: "api_error", Message: "upstream stream interrupted"},
			})

			break
		}
		if ev.Done {
			break
		}
		var se streamEvent
		if jsonErr := json.Unmarshal([]byte(ev.Data), &se); jsonErr != nil {
			continue
		}
		if !started { // first event — open the message (headers lazily)
			started = true
			emit(models.AnthropicEvent{
				Type: "message_start",
				Message: &models.AnthropicMessagePayload{
					ID: "resp", Type: typeMessage, Role: roleAssistantS, Model: model, Content: []any{},
				},
			})
		}
		switch se.Type {
		case "response.output_item.added":
			if se.Item != nil {
				id := se.Item.ID
				if id == "" {
					id = se.Item.CallID
				}
				switch se.Item.Type {
				case "function_call":
					openBlock(id, models.AnthropicBlock{Type: typeToolUse, ID: se.Item.CallID, Name: se.Item.Name})
				case responsesItemWebSearchCall:
					// hosted search: no client block — collected at item.done
				default:
					openBlock(id, models.AnthropicBlock{Type: typeText})
				}
			}
		case "response.output_item.done":
			// only the done item carries the action (added is status-only)
			if se.Item != nil && se.Item.Type == responsesItemWebSearchCall && se.Item.Action != nil {
				search.onCallDone(se.Item.ID, se.Item.Action.Type, se.Item.Action.Query)
			}
		case "response.output_text.annotation.added":
			if se.Annotation != nil {
				search.onAnnotation(se.Annotation.Type, se.Annotation.URL, se.Annotation.Title)
			}
		case "response.output_text.delta":
			if _, ok := itemIdx[se.ItemID]; !ok {
				// a delta without output_item.added — a lazy text block
				openBlock(se.ItemID, models.AnthropicBlock{Type: typeText})
			}
			idx := itemIdx[se.ItemID]
			delta := models.AnthropicDelta{Type: "text_delta", Text: se.Delta}
			emit(models.AnthropicEvent{Type: "content_block_delta", Index: &idx, Delta: &delta})
		case "response.function_call_arguments.delta":
			if idx, ok := itemIdx[se.ItemID]; ok {
				delta := models.AnthropicDelta{Type: "input_json_delta", PartialJSON: se.Delta}
				emit(models.AnthropicEvent{Type: "content_block_delta", Index: &idx, Delta: &delta})
			}
		case "response.completed":
			if se.Response != nil && se.Response.Usage != nil {
				usage = *se.Response.Usage
			}
			for _, id := range itemOrder {
				if itemIsTool[id] {
					stopReason = "tool_use"

					break
				}
			}
			// the search blocks open last: annotations are complete only
			// here, and the closing loop below stops them in order
			for _, blk := range search.blocks() {
				openBlock(blk.itemID, blk.block)
			}
		case "response.failed":
			msg := "response failed"
			if se.Response != nil && se.Response.Error != nil && se.Response.Error.Message != "" {
				msg = se.Response.Error.Message
			}
			emit(models.AnthropicEvent{
				Type:  evError,
				Error: &models.AnthropicError{Type: "api_error", Message: msg},
			})
		case evError:
			emit(models.AnthropicEvent{
				Type:  evError,
				Error: &models.AnthropicError{Type: "api_error", Message: se.Delta},
			})
		default:
			// other Responses events are ignored
		}
	}
	if !started {
		return false
	}
	// close ALL blocks in opening order
	for _, id := range itemOrder {
		idx := itemIdx[id]
		emit(models.AnthropicEvent{Type: "content_block_stop", Index: &idx})
	}
	emit(models.AnthropicEvent{
		Type:  "message_delta",
		Delta: &models.AnthropicDelta{Type: "message_delta", StopReason: stopReason},
		Usage: &usage,
	})
	emit(models.AnthropicEvent{Type: "message_stop"})

	return true
}
