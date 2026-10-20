package llm

import (
	"fmt"
	"strings"
)

// textOnlyModels are model families that accept text only. The list is
// conservative: an unknown model is assumed to support images, and a
// provider rejection still retries once without them.
var textOnlyModels = []string{"beam", "longcat"}

// TextOnlyModel reports whether a model is known to reject image input.
func TextOnlyModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	for _, prefix := range textOnlyModels {
		if strings.HasPrefix(m, prefix) {
			return true
		}
	}
	return false
}

// SupportsImages reports whether the model should receive image attachments.
func SupportsImages(model string) bool {
	return !TextOnlyModel(model)
}

// IsImageUnsupported reports whether a provider rejected a request because
// the model cannot see images.
func IsImageUnsupported(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "supports text input only") ||
		strings.Contains(s, "contains an image") ||
		strings.Contains(s, "does not support image") ||
		strings.Contains(s, "image input is not supported") {
		return true
	}
	return strings.Contains(s, "image") &&
		(strings.Contains(s, "invalid_value") || strings.Contains(s, "unsupported"))
}

// TextOnlyMessages returns a copy of msgs safe for a text-only model: image,
// video, and audio attachments are removed and replaced with a short text
// note. Text documents are kept because the provider sends them as text.
func TextOnlyMessages(msgs []Message) []Message {
	out := make([]Message, len(msgs))
	for i, m := range msgs {
		out[i] = m
		if len(m.Attachments) == 0 {
			continue
		}
		kept := make([]Attachment, 0, len(m.Attachments))
		var omitted []string
		for _, a := range m.Attachments {
			switch a.Kind {
			case "image", "video", "audio":
				name := a.Name
				if name == "" {
					name = a.Path
				}
				omitted = append(omitted, fmt.Sprintf("%s %s at %s", a.Kind, name, a.Path))
			default:
				kept = append(kept, a)
			}
		}
		if len(omitted) == 0 {
			continue
		}
		note := "[attachment omitted: the current model accepts text only; " + strings.Join(omitted, "; ") + "]"
		if strings.TrimSpace(m.Content) == "" {
			out[i].Content = note
		} else {
			out[i].Content = m.Content + "\n\n" + note
		}
		out[i].Attachments = kept
	}
	return out
}
