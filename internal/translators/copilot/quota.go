package copilot

// Quota marking: the gateway bills one premium interaction per
// user-initiated turn. Tool continuations, context compaction and subagent
// turns are continuations of an already-billed interaction — the request
// must say so (x-initiator: agent), or every tool turn re-bills.

// quotaClass is the request's billing classification.
type quotaClass struct {
	// Initiator is "user" (bills a premium interaction) or "agent"
	// (continuation of the running interaction).
	Initiator string
	// IsCompact: the client compacting its own context.
	IsCompact bool
	// IsSubagent: a subagent turn spawned by the Agent tool.
	IsSubagent bool
}

const (
	initiatorUser  = "user"
	initiatorAgent = "agent"
	// the same strings double as message roles and block types
	blockTypeText       = "text"
	blockTypeToolResult = "tool_result"
)

// classifyQuota decides the billing class from the request shape. Only the
// LAST message matters: a user message whose content carries any tool_result
// is a tool continuation (mixed tool_result+text shapes included — skills,
// edit hooks and plan reminders all send those); anything else the user
// themselves typed is a fresh interaction. Subagent and compact turns are
// agent-initiated regardless of their last message.
func classifyQuota(m map[string]any) quotaClass {
	cls := quotaClass{Initiator: initiatorUser}
	cls.IsSubagent = detectSubagent(m)
	cls.IsCompact = isCompactRequest(m)

	messages := asSlice(m["messages"])
	if len(messages) == 0 {
		return cls
	}
	last := asMap(messages[len(messages)-1])
	if last != nil && last["role"] == initiatorUser && carriesToolResult(last) {
		cls.Initiator = initiatorAgent
	}
	if cls.IsSubagent || cls.IsCompact {
		cls.Initiator = initiatorAgent
	}

	return cls
}

// carriesToolResult: the message content holds at least one tool_result
// block (mixed shapes count — see classifyQuota).
func carriesToolResult(msg map[string]any) bool {
	for _, b := range asSlice(msg["content"]) {
		if blk := asMap(b); blk != nil && blk["type"] == blockTypeToolResult {
			return true
		}
	}

	return false
}
