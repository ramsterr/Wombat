package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestQuestionAsksTheUser(t *testing.T) {
	asked := []Question{}
	env := &Env{
		Dir: t.TempDir(),
		AskUser: func(q Question) (string, error) {
			asked = append(asked, q)
			return "Editorial / magazine", nil
		},
	}
	out, err := (&QuestionTool{}).Run(context.Background(), json.RawMessage(`{"questions":[
		{"id":"style","header":"direction","question":"Which direction should the poster take?","options":[
			{"label":"Editorial / magazine","description":"Bold serif headline."},
			{"label":"Minimal / gallery","description":"Lots of white space."}]}
	]}`), env)
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 {
		t.Fatalf("asked %d questions", len(asked))
	}
	if asked[0].Question == "" || len(asked[0].Options) != 2 {
		t.Errorf("question = %+v", asked[0])
	}
	if !strings.Contains(out, "Editorial / magazine") {
		t.Errorf("the answer should reach the model: %s", out)
	}
}

func TestQuestionNeedsSomewhereToAsk(t *testing.T) {
	env := &Env{Dir: t.TempDir()}
	_, err := (&QuestionTool{}).Run(context.Background(), json.RawMessage(`{"questions":[
		{"question":"Which one?","options":[{"label":"a"},{"label":"b"}]}]}`), env)
	if err == nil {
		t.Fatal("expected an error when nobody can answer")
	}
}

func TestFormatQuestion(t *testing.T) {
	q := Question{Header: "direction", Question: "Which one?", Options: []QuestionOption{
		{Label: "a", Description: "first"},
		{Label: "b"},
	}}
	text := FormatQuestion(q)
	for _, want := range []string{"direction", "Which one?", "1) a - first", "2) b", "type your own"} {
		if !strings.Contains(text, want) {
			t.Errorf("the question text is missing %q:\n%s", want, text)
		}
	}
}
