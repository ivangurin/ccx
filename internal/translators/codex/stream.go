package codex

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/reqdump"
	"github.com/ivangurin/ccx/internal/pkg/sse"
	"github.com/ivangurin/ccx/internal/pkg/usagelog"
	"github.com/ivangurin/ccx/internal/translators/relay"
)

// streamEvent is a typed Responses SSE event.
type streamEvent struct {
	Type    string `json:"type"`
	Delta   string `json:"delta"`
	Message string `json:"message"` // the error event's text (not a delta)
	ItemID  string `json:"item_id"`
	// Arguments — the COMPLETE function arguments, carried by
	// response.function_call_arguments.done (top level) and by the function_call
	// items of output_item.added/done. Parallel calls never emit argument
	// deltas at all — these carriers are the only source (verified live
	// 2026-11-08); single calls stream .delta fragments and then repeat the
	// full string in the done carriers, which must be ignored there.
	Arguments string `json:"arguments"`
	Item      *struct {
		Type      string `json:"type"`
		Name      string `json:"name"`
		CallID    string `json:"call_id"`
		ID        string `json:"id"`
		Arguments string `json:"arguments"`
		Action    *struct {
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
		// incomplete_details rides response.incomplete (the documented third
		// terminal event, e.g. content_filter / max_output_tokens)
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	} `json:"response"`
}

// serveStream relays the upstream Responses SSE to the client as Anthropic
// SSE (lazy writer: an empty stream is a 502 JSON, not an empty SSE). The
// usage entry picks up TTFT (first emitted event) and the terminal usage.
func (t *Translator) serveStream(w http.ResponseWriter, resp *http.Response, model string, entry *usagelog.Entry) {
	var writer *sse.Writer
	relayStarted := time.Now()
	var ttft time.Duration
	var usage models.Usage
	started := t.streamEvents(resp, model, func(ev models.AnthropicEvent) {
		if writer == nil {
			writer = sse.NewWriter(w)
		}
		if ttft == 0 {
			ttft = time.Since(relayStarted)
		}
		if ev.Type == evMessageDelta && ev.Usage != nil {
			usage = *ev.Usage
		}
		// a stream error (mid-relay break or a backend error event) must not
		// stay a clean 200 in the usage log
		if ev.Type == evError && ev.Error != nil && ev.Error.Message != "" {
			entry.Status, entry.Error = http.StatusBadGateway, ev.Error.Message
		}
		writer.Event(ev.Type, ev)
	})
	if !started {
		entry.Status, entry.Error = http.StatusBadGateway, "codex: empty upstream stream"
		relay.WriteErr(w, http.StatusBadGateway, "codex: empty upstream stream")

		return
	}
	writer.Close()
	entry.TTFTMS = ttft.Milliseconds()
	entry.InputTokens, entry.OutputTokens = usage.InputTokens, usage.OutputTokens
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}

	return ""
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
	// arguments already delivered per item: streamed = any .delta fragment was
	// forwarded (fragments concatenate, every one must pass), sealed = the full
	// string was delivered once by a done carrier (a second carrier of the same
	// arguments must not append a copy — input_json_delta concatenates client-side)
	argsStreamed := map[string]bool{}
	argsSealed := map[string]bool{}
	var usage models.Usage
	stopReason := "end_turn"
	var search webSearchCollector
	started := false
	// ended — the stream's fate is known: a terminal event
	// (completed/failed/incomplete) or an already-relayed stream error. A
	// clean EOF without one is an upstream truncation — observed live
	// 2026-11-08 (17:42-17:45: 200 + an EMPTY assistant message on every
	// codex request; the usage log's in=0/out=0 was the tell) — and must
	// surface as an error, never as a healthy empty answer.
	ended := false

	openBlock := func(itemID string, block models.AnthropicBlock) {
		idx := nextIndex
		nextIndex++
		itemIdx[itemID] = idx
		itemIsTool[itemID] = block.Type == typeToolUse
		itemOrder = append(itemOrder, itemID)
		emit(models.AnthropicEvent{Type: "content_block_start", Index: &idx, ContentBlock: &block})
	}

	// emitArgs delivers a COMPLETE arguments string exactly once: only when no
	// fragment has streamed and no carrier has fired yet. The empty string is
	// not a delivery (output_item.added carries arguments:"" routinely).
	emitArgs := func(itemID, args string) {
		if args == "" || argsStreamed[itemID] || argsSealed[itemID] {
			return
		}
		idx, ok := itemIdx[itemID]
		if !ok {
			return
		}
		argsSealed[itemID] = true
		delta := models.AnthropicDelta{Type: "input_json_delta", PartialJSON: args}
		emit(models.AnthropicEvent{Type: evContentBlockDelta, Index: &idx, Delta: &delta})
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
			ended = true // the error is relayed; no truncation error after the loop

			break
		}
		if ev.Done {
			break
		}
		if reqdump.Enabled() {
			reqdump.Write("codex-sse", []byte(ev.Data))
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
					emitArgs(id, se.Item.Arguments)
				case responsesItemWebSearchCall:
					// hosted search: no client block — collected at item.done
				default:
					openBlock(id, models.AnthropicBlock{Type: typeText})
				}
			}
		case "response.output_item.done":
			if se.Item == nil {
				break
			}
			switch se.Item.Type {
			case "function_call":
				// the complete item is the authoritative arguments carrier for
				// calls that never streamed deltas (parallel calls)
				id := se.Item.ID
				if id == "" {
					id = se.Item.CallID
				}
				emitArgs(id, se.Item.Arguments)
			case responsesItemWebSearchCall:
				// only the done item carries the action (added is status-only)
				if se.Item.Action != nil {
					search.onCallDone(se.Item.ID, se.Item.Action.Type, se.Item.Action.Query)
				}
			default:
				// other done items (message, reasoning…) carry nothing extra
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
			emit(models.AnthropicEvent{Type: evContentBlockDelta, Index: &idx, Delta: &delta})
		case "response.function_call_arguments.delta":
			// a sealed item already carries its full arguments — a late fragment
			// would duplicate them
			if idx, ok := itemIdx[se.ItemID]; ok && !argsSealed[se.ItemID] {
				argsStreamed[se.ItemID] = true
				delta := models.AnthropicDelta{Type: "input_json_delta", PartialJSON: se.Delta}
				emit(models.AnthropicEvent{Type: evContentBlockDelta, Index: &idx, Delta: &delta})
			}
		case "response.function_call_arguments.done":
			emitArgs(se.ItemID, se.Arguments)
		case "response.completed":
			ended = true
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
			ended = true
			msg := "response failed"
			if se.Response != nil && se.Response.Error != nil && se.Response.Error.Message != "" {
				msg = se.Response.Error.Message
			}
			emit(models.AnthropicEvent{
				Type:  evError,
				Error: &models.AnthropicError{Type: "api_error", Message: msg},
			})
		case "response.incomplete":
			// a terminal event too: an incomplete answer (content filter,
			// output cap) is not an answer — capture the usage it still
			// reports and surface the reason as an error
			ended = true
			if se.Response != nil && se.Response.Usage != nil {
				usage = *se.Response.Usage
			}
			msg := "upstream response incomplete"
			if se.Response != nil && se.Response.IncompleteDetails != nil && se.Response.IncompleteDetails.Reason != "" {
				msg += ": " + se.Response.IncompleteDetails.Reason
			}
			emit(models.AnthropicEvent{
				Type:  evError,
				Error: &models.AnthropicError{Type: "api_error", Message: msg},
			})
		case evError:
			emit(models.AnthropicEvent{
				Type:  evError,
				Error: &models.AnthropicError{Type: "api_error", Message: firstNonEmpty(se.Message, se.Delta)},
			})
		default:
			// other Responses events are ignored
		}
	}
	if !started {
		return false
	}
	if !ended {
		// a clean end (EOF/[DONE]) without a terminal event — the upstream
		// truncated the stream; the error event keeps it a visible failure in
		// the client AND in the usage log (serveStream maps evError to 502)
		emit(models.AnthropicEvent{
			Type:  evError,
			Error: &models.AnthropicError{Type: "api_error", Message: "upstream stream ended without completion"},
		})
	}
	// close ALL blocks in opening order
	for _, id := range itemOrder {
		idx := itemIdx[id]
		emit(models.AnthropicEvent{Type: "content_block_stop", Index: &idx})
	}
	emit(models.AnthropicEvent{
		Type:  evMessageDelta,
		Delta: &models.AnthropicDelta{Type: evMessageDelta, StopReason: stopReason},
		Usage: &usage,
	})
	emit(models.AnthropicEvent{Type: "message_stop"})

	return true
}
