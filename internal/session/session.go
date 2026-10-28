package session

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
	// Agents lists the custom agents selected for this session, so resuming
	// restores the same team.
	Agents []string `json:"agents,omitempty"`
}

type record struct {
	Type string      `json:"type"`
	Msg  llm.Message `json:"msg,omitempty"`
}

type Session struct {
	mu       sync.Mutex
	Meta     Meta
	Messages []llm.Message
	path     string
}

func projectDir(cwd string) string {
	return projectDirAt(config.Home(), cwd)
}

func projectDirAt(home, cwd string) string {
	h := sha1.Sum([]byte(cwd))
	return filepath.Join(home, "sessions", hex.EncodeToString(h[:])[:12])
}

func newID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

func Create(cwd, provider, model, title string) (*Session, error) {
	return CreateAt(config.Home(), cwd, provider, model, title)
}

func CreateAt(home, cwd, provider, model, title string) (*Session, error) {
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
	s.path = filepath.Join(projectDirAt(home, cwd), s.Meta.ID+".jsonl")
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
			s.Messages = append(s.Messages, pruneEphemeralAttachments(r.Msg))
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

// pruneEphemeralAttachments drops images that lived only in memory when the
// session was live, such as browser screenshots, and attachments whose file
// has since been moved or deleted. Neither can be loaded again.
func pruneEphemeralAttachments(m llm.Message) llm.Message {
	return pruneUnavailableAttachments(m)
}

// pruneUnavailableAttachments removes attachments that no longer have bytes
// to send: in-memory media that was never stored, and path-backed files that
// were deleted or moved. A note is left in the message so the model knows
// something was dropped.
func pruneUnavailableAttachments(m llm.Message) llm.Message {
	if len(m.Attachments) == 0 {
		return m
	}
	keep := m.Attachments[:0]
	var missing []string
	for _, a := range m.Attachments {
		if len(a.Data) > 0 {
			keep = append(keep, a)
			continue
		}
		if a.Path == "" {
			continue
		}
		if _, err := os.Stat(a.Path); err != nil {
			missing = append(missing, attachmentName(a))
			continue
		}
		keep = append(keep, a)
	}
	if len(keep) == 0 {
		m.Attachments = nil
	} else {
		m.Attachments = keep
	}
	for _, name := range missing {
		note := "[attachment unavailable: " + name + "]"
		if !strings.Contains(m.Content, note) {
			m.Content = strings.TrimSpace(m.Content + "\n\n" + note)
		}
	}
	return m
}

func attachmentName(a llm.Attachment) string {
	if a.Name != "" {
		return a.Name
	}
	if a.Path != "" {
		return filepath.Base(a.Path)
	}
	return "file"
}

// PruneUnavailableAttachments removes attachments whose files are gone and
// persists the cleaned history. A session can otherwise fail every request
// after an image it read is deleted, because the provider rebuilds the
// attachment from its path on each turn.
func (s *Session) PruneUnavailableAttachments() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dropped := 0
	for i := range s.Messages {
		before := len(s.Messages[i].Attachments)
		content := s.Messages[i].Content
		s.Messages[i] = pruneUnavailableAttachments(s.Messages[i])
		dropped += before - len(s.Messages[i].Attachments)
		if s.Messages[i].Content != content {
			s.Meta.Updated = time.Now()
		}
	}
	if dropped == 0 {
		return 0, nil
	}
	return dropped, s.flushLocked()
}

func (s *Session) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Meta.ID
}

func (s *Session) Path() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.path
}

// StoreAttachment copies src into the project's attachment store and returns
// metadata referencing the stored copy. Files are deduplicated by content.
func (s *Session) StoreAttachment(src string) (llm.Attachment, error) {
	a, err := llm.NewAttachment(src)
	if err != nil {
		return llm.Attachment{}, err
	}
	dir := filepath.Join(filepath.Dir(s.path), ".attachments")
	if filepath.Dir(a.Path) == dir {
		return a, nil
	}
	data, err := llm.LoadAttachment(a)
	if err != nil {
		return llm.Attachment{}, err
	}
	h := sha256.Sum256(data)
	dst := filepath.Join(dir, hex.EncodeToString(h[:8])+"-"+safeAttachmentName(a.Name))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return llm.Attachment{}, err
	}
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			return llm.Attachment{}, err
		}
	} else if err != nil {
		return llm.Attachment{}, err
	}
	a.Path = dst
	return a, nil
}

func safeAttachmentName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._-")
	if out == "" {
		out = "file"
	}
	if len(out) > 80 {
		out = out[len(out)-80:]
	}
	return out
}

func (s *Session) Append(m llm.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Messages = msgs
	s.Meta.Updated = time.Now()
	return s.flushLocked()
}

func (s *Session) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushLocked()
}

func (s *Session) flushLocked() error {
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
