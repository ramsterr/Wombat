package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Question is one decision the user has to make.
type Question struct {
	// ID is an optional short identifier.
	ID string `json:"id,omitempty"`
	// Question is the sentence shown to the user.
	Question string `json:"question"`
	// Header is a short label for the question.
	Header string `json:"header,omitempty"`
	// Options are the concrete choices. The user can always type their own.
	Options []QuestionOption `json:"options"`
	// MultiSelect lets the user pick more than one.
	MultiSelect bool `json:"multi_select,omitempty"`
}

// QuestionOption is one choice.
type QuestionOption struct {
	// Label is the short name of the choice.
	Label string `json:"label"`
	// Description explains what picking it means.
	Description string `json:"description,omitempty"`
}

// QuestionTool asks the user to choose. It is the agent's way of handing a
// decision back instead of guessing or polishing on its own.
type QuestionTool struct{}

type questionArgs struct {
	Questions []Question `json:"questions"`
}

func (t *QuestionTool) Name() string  { return "question" }
func (t *QuestionTool) Class() string { return "ask" }
func (t *QuestionTool) Description() string {
	return "Ask the user to make a decision. Use it when the output you want depends on a choice only the user can make " +
		"(which direction, which option, what \"good\" means here). Do not use it for final preview feedback; the harness asks when the run is ready to hand off. " +
		"Give 2-3 concrete options with a short description of each; the user can also type their own answer. " +
		"Ask once with everything the user needs, then act on the answer."
}
func (t *QuestionTool) Schema() map[string]any {
	return obj(map[string]any{
		"questions": map[string]any{
			"type":        "array",
			"description": "the decisions you need from the user",
			"minItems":    1,
			"maxItems":    5,
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":       map[string]any{"type": "string", "description": "optional short id"},
					"header":   map[string]any{"type": "string", "description": "short label, for example \"style\""},
					"question": map[string]any{"type": "string", "description": "the question to ask, as one clear sentence"},
					"options": map[string]any{
						"type":        "array",
						"description": "2-3 concrete choices, each with a label and what it means",
						"minItems":    2,
						"maxItems":    5,
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"label":       map[string]any{"type": "string", "description": "the choice, in a few words"},
								"description": map[string]any{"type": "string", "description": "what this choice means for the result"},
							},
							"required": []string{"label"},
						},
					},
					"multi_select": map[string]any{"type": "boolean", "description": "let the user pick more than one"},
				},
				"required": []string{"question", "options"},
			},
		},
	}, "questions")
}

func (t *QuestionTool) Run(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
	var a questionArgs
	if err := decodeInto(raw, &a); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	if len(a.Questions) == 0 {
		return "", errors.New("question needs at least one question")
	}
	if env == nil || env.AskUser == nil {
		return "", errors.New("the user cannot be asked here - do the work yourself or say what you need in your answer")
	}
	if err := env.AllowQuestion(); err != nil {
		return "", err
	}
	answers := make([]map[string]any, 0, len(a.Questions))
	for _, q := range a.Questions {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if previewFeedbackQuestion(q) {
			return "", errors.New("do not ask for preview feedback; finish the complete deliverable and let the harness ask at the end of the run")
		}
		answer, err := env.AskUser(q)
		if err != nil {
			return "", err
		}
		answers = append(answers, map[string]any{
			"id":       q.ID,
			"question": q.Question,
			"answer":   answer,
		})
	}
	out, _ := json.MarshalIndent(map[string]any{"answers": answers}, "", "  ")
	return string(out), nil
}

// decodeInto is decode with a clearer error.
func decodeInto(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return errors.New("no arguments")
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return err
	}
	return nil
}

func previewFeedbackQuestion(q Question) bool {
	if strings.EqualFold(strings.TrimSpace(q.ID), "visual-review") {
		return true
	}
	var text strings.Builder
	text.WriteString(q.Header)
	text.WriteString(" ")
	text.WriteString(q.Question)
	for _, o := range q.Options {
		text.WriteString(" ")
		text.WriteString(o.Label)
	}
	lower := strings.ToLower(text.String())
	for _, marker := range []string{"looks good", "needs changes", "look right", "looks right", "preview feedback", "review the preview", "ship it"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// FormatQuestion renders a question for a plain-text interface.
func FormatQuestion(q Question) string {
	var b strings.Builder
	if q.Header != "" {
		b.WriteString(q.Header + "\n")
	}
	b.WriteString(q.Question + "\n")
	for i, o := range q.Options {
		b.WriteString(fmt.Sprintf("  %d) %s", i+1, o.Label))
		if o.Description != "" {
			b.WriteString(" - " + o.Description)
		}
		b.WriteString("\n")
	}
	b.WriteString("  (or type your own answer)")
	return b.String()
}
