package cli

import (
	"encoding/json"
	"io"
	"testing"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/llm"
)

func TestBridgeDetachByIndex(t *testing.T) {
	b := &bridge{
		cfg: &config.Config{Providers: map[string]*config.Provider{}},
		out: json.NewEncoder(io.Discard),
		atts: []llm.Attachment{
			{Name: "first.png", Path: "/tmp/first.png"},
			{Name: "second.pdf", Path: "/tmp/second.pdf"},
		},
	}

	b.detach("1")
	if len(b.atts) != 1 || b.atts[0].Name != "second.pdf" {
		t.Fatalf("attachments after index detach = %+v", b.atts)
	}

	b.detach("1")
	if len(b.atts) != 0 {
		t.Fatalf("attachments after second detach = %+v", b.atts)
	}
}

func TestBridgeDetachAll(t *testing.T) {
	b := &bridge{
		cfg: &config.Config{Providers: map[string]*config.Provider{}},
		out: json.NewEncoder(io.Discard),
		atts: []llm.Attachment{
			{Name: "first.png", Path: "/tmp/first.png"},
			{Name: "second.pdf", Path: "/tmp/second.pdf"},
		},
	}

	b.detach("all")
	if len(b.atts) != 0 {
		t.Fatalf("attachments after detach all = %+v", b.atts)
	}
}
