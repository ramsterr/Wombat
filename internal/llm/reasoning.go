package llm

// TrimStaleReasoning returns the messages to send for a request, dropping
// the reasoning of every assistant turn except the most recent one.
//
// Why: reasoning (thinking) tokens are billed as output tokens when the model
// produces them, and they are re-sent as input tokens on every later request
// if they stay in the history. Only the newest assistant turn needs them - it
// is the turn whose tool results the model is continuing - so the older ones
// are pure cost.
//
// The session keeps the full text, so the transcript, /history, and a resumed
// session still show the thinking.
func TrimStaleReasoning(msgs []Message) []Message {
	last := lastAssistantIndex(msgs)
	if last < 0 {
		return msgs
	}
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if i == last || out[i].Role != "assistant" {
			continue
		}
		out[i].Reasoning = ""
		out[i].ReasoningItems = nil
	}
	return out
}

// lastAssistantIndex returns the index of the most recent assistant message.
func lastAssistantIndex(msgs []Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" {
			return i
		}
	}
	return -1
}
