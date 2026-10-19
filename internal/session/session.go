package session

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/llm"
)

type Meta struct {
	Type     string    `json:"type"`
	ID       string    `json:"id"`
	Cwd      string    `json:"cwd"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	Provider string    `json:"provider"`
	Model    string    `json:"model"`
	Title    string    `json:"title,omitempty"`
	Turns    int       `json:"turns,omitempty"`
}

type record struct {
	Type string      `json:"type"`
	Msg  llm.Message `json:"msg,omitempty"`
}

type Session struct {
	Meta     Meta
	Messages []llm.Message
	path     string
}

func projectDir(cwd string) string {
	h := sha1.Sum([]byte(cwd))
	return filepath.Join(config.Home(), "sessions", hex.EncodeToString(h[:])[:12])
}

func newID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

func Create(cwd, provider, model, title string) (*Session, error) {
	s := &Session{
		Meta: Meta{
			Type:     "meta",
			ID:       newID(),
			Cwd:      cwd,
			Created:  time.Now(),
			Updated:  time.Now(),
			Provider: provider,
			Model:    model,
			Title:    title,
		},
	}
	s.path = filepath.Join(projectDir(cwd), s.Meta.ID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return nil, err
	}
	return s, s.Flush()
}

func Open(id, cwd string) (*Session, error) {
	path := filepath.Join(projectDir(cwd), id+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s := &Session{path: path}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if first {
			if err := json.Unmarshal([]byte(line), &s.Meta); err != nil {
				return nil, fmt.Errorf("corrupt session file: %w", err)
			}
			first = false
			continue
		}
		var r record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		if r.Type == "msg" {
			s.Messages = append(s.Messages, r.Msg)
		}
	}
	if first {
		return nil, fmt.Errorf("empty session file")
	}
	return s, sc.Err()
}

func Latest(cwd string) (*Meta, error) {
	metas, err := List(cwd)
	if err != nil {
		return nil, err
	}
	if len(metas) == 0 {
		return nil, os.ErrNotExist
	}
	return &metas[0], nil
}

func List(cwd string) ([]Meta, error) {
	dir := projectDir(cwd)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var metas []Meta
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		if sc.Scan() {
			var m Meta
			if json.Unmarshal([]byte(sc.Text()), &m) == nil {
				metas = append(metas, m)
			}
		}
		f.Close()
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].Updated.After(metas[j].Updated) })
	return metas, nil
}

func (s *Session) ID() string { return s.Meta.ID }

func (s *Session) Append(m llm.Message) error {
	s.Messages = append(s.Messages, m)
	s.Meta.Updated = time.Now()
	if m.Role == "user" && !strings.HasPrefix(m.Content, "Today: ") && !strings.HasPrefix(m.Content, "[Conversation summary]") {
		s.Meta.Turns++
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	raw, err := json.Marshal(record{Type: "msg", Msg: m})
	if err != nil {
		return err
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return err
	}
	return nil
}

func (s *Session) ReplaceMessages(msgs []llm.Message) error {
	s.Messages = msgs
	s.Meta.Updated = time.Now()
	return s.Flush()
}

func (s *Session) Flush() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	meta, err := json.Marshal(s.Meta)
	if err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(append(meta, '\n')); err != nil {
		f.Close()
		return err
	}
	for _, m := range s.Messages {
		raw, err := json.Marshal(record{Type: "msg", Msg: m})
		if err != nil {
			f.Close()
			return err
		}
		if _, err := f.Write(append(raw, '\n')); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
