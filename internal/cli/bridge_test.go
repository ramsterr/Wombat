package cli

import (
	"encoding/json"
	"io"
	"testing"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
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

func TestSelectProviderUpdatesSession(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	cwd := t.TempDir()
	sess, err := session.Create(cwd, "old", "old-model", "")
	if err != nil {
		t.Fatal(err)
	}
	b := &bridge{
		cfg:  &config.Config{Provider: "new", Model: "new-model"},
		sess: sess,
		out:  json.NewEncoder(io.Discard),
	}

	b.selectProvider("new")

	if got := b.sess.Meta.Provider; got != "new" {
		t.Fatalf("session provider = %q", got)
	}
	if got := b.state()["provider"]; got != "new" {
		t.Fatalf("state provider = %v", got)
	}
	reopened, err := session.Open(sess.ID(), cwd)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Meta.Provider; got != "new" {
		t.Fatalf("persisted provider = %q", got)
	}
}
