package copilot

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Deterministic request/interaction ids: the gateway aggregates requests
// that share an interaction id, and the client retries the same logical
// request when its transport hiccups — a fresh random id per attempt bills
// each retry as its own interaction. The ids therefore derive from the
// conversation (session) and, for the request id, the last user text: same
// logical turn → same id, next turn → next id.

// quotaIDs returns the request and interaction ids for one request. With no
// resolvable session there is nothing stable to derive from — random ids
// (still UUID-shaped; the gateway requires the shape).
func quotaIDs(sessionID string, m map[string]any) (requestID, interactionID string) {
	if sessionID == "" {
		return randomUUID(), randomUUID()
	}

	return deterministicUUID(sessionID + "\x00" + lastUserTurnSeed(m)), deterministicUUID(sessionID)
}

// lastUserTurnSeed identifies the logical turn: the last user message's text
// AND the tool_use ids its tool_results answer. Text alone collapses on
// pure tool continuations (no loose text — the merged shape), ids alone
// collapse on typed follow-ups; together a distinct turn is a distinct seed
// and a retried turn keeps its seed.
func lastUserTurnSeed(m map[string]any) string {
	messages := asSlice(m["messages"])
	for i := len(messages) - 1; i >= 0; i-- {
		msg := asMap(messages[i])
		if msg == nil || msg["role"] != initiatorUser {
			continue
		}
		var ids []string
		for _, raw := range asSlice(msg["content"]) {
			if blk := asMap(raw); blk != nil && blk["type"] == blockTypeToolResult {
				ids = append(ids, asString(blk["tool_use_id"]))
			}
		}

		return messageText(msg) + "\x1f" + strings.Join(ids, "\x1f")
	}

	return ""
}

// deterministicUUID derives a UUIDv4-shaped id from the seed: sha256, first
// 16 bytes, version/variant bits forced to the v4 shape (shape matters to
// the gateway, derivability matters to us — not a real v4 and not meant to
// be one).
func deterministicUUID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	buf := make([]byte, 36)
	hex.Encode(buf[0:8], b[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], b[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], b[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], b[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], b[10:16])

	return string(buf)
}

func randomUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure leaves zero bytes — still a valid shape; a
		// billing id colliding at the zero id is better than dropping the
		// request over id generation
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
