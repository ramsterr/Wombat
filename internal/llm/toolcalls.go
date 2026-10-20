package llm

import "fmt"

// interruptedToolResult is the result a provider sees for a tool call that
// never got a real response. This happens when a session was interrupted
// between the assistant's tool_calls message and the tool results, or when a
// user message (an attachment) was written between tool results.
const interruptedToolResult = "error: the tool call was interrupted before a result was recorded. " +
	"Re-run it if you still need the answer."

// NormalizeToolCalls returns a copy of msgs in which every assistant message
// with tool calls is followed by exactly one tool message per tool_call_id,
// before any non-tool message. Providers require that shape; a stale session
// from an interrupted run, or a user message inserted between tool results,
// otherwise fails with "insufficient tool messages following tool_calls".
//
// Real tool results are kept. Missing results are filled with an error tool
// message, and orphan tool messages (ones whose tool call is gone) are
// dropped. The session itself is not modified.
func NormalizeToolCalls(msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	var pending []ToolCall
	repair := 0
	seen := map[string]bool{}

	flush := func() {
		for _, tc := range pending {
			out = append(out, Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    interruptedToolResult,
			})
		}
		pending = nil
	}

	for _, m := range msgs {
		switch m.Role {
		case "assistant":
			flush()
			if len(m.ToolCalls) > 0 {
				calls := make([]ToolCall, len(m.ToolCalls))
				copy(calls, m.ToolCalls)
				for i := range calls {
					if calls[i].ID == "" || seen[calls[i].ID] {
						repair++
						calls[i].ID = fmt.Sprintf("call_repair_%d", repair)
					}
					seen[calls[i].ID] = true
				}
				m.ToolCalls = calls
				pending = append(pending, calls...)
			}
			out = append(out, m)
		case "tool":
			if len(pending) == 0 {
				continue // orphan result: its assistant tool call is gone
			}
			idx := matchPending(pending, m)
			if idx < 0 {
				continue // not a response to any pending call
			}
			// Fill earlier calls first so the result order matches the
			// assistant's tool call order.
			for i := 0; i < idx; i++ {
				out = append(out, Message{
					Role:       "tool",
					ToolCallID: pending[i].ID,
					Name:       pending[i].Name,
					Content:    interruptedToolResult,
				})
			}
			if m.ToolCallID != pending[idx].ID {
				m.ToolCallID = pending[idx].ID
			}
			if m.Name == "" {
				m.Name = pending[idx].Name
			}
			out = append(out, m)
			pending = pending[idx+1:]
		default:
			flush()
			out = append(out, m)
		}
	}
	flush()
	return out
}

// matchPending finds the pending tool call a result belongs to. IDs are the
// primary key; name and order are fallbacks for providers that omit IDs.
func matchPending(pending []ToolCall, m Message) int {
	if m.ToolCallID != "" {
		for i, tc := range pending {
			if tc.ID == m.ToolCallID {
				return i
			}
		}
	}
	if m.Name != "" {
		for i, tc := range pending {
			if tc.Name == m.Name {
				return i
			}
		}
	}
	if len(pending) == 1 {
		return 0
	}
	return -1
}
