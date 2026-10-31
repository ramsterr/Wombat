// Command faultsrv is a scripted OpenAI-compatible server used to inject
// transport faults into the real agent loop. It plays the part of the model:
// it answers the first request with a write tool call that fixes the eval
// fixture, and every later request with a short final message.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

const solution = "package fixture\n\nfunc Sum(nums []int) int {\n\ttotal := 0\n\tfor _, n := range nums {\n\t\ttotal += n\n\t}\n\treturn total\n}\n"

type stats struct {
	mu       sync.Mutex
	requests int
	logw     io.Writer
}

func (s *stats) record(entry map[string]any) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++
	entry["n"] = s.requests
	entry["at"] = time.Now().Format(time.RFC3339Nano)
	if s.logw != nil {
		raw, _ := json.Marshal(entry)
		fmt.Fprintf(s.logw, "%s\n", raw)
	}
	return s.requests
}

func (s *stats) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

type request struct {
	Model    string `json:"model"`
	Messages []struct {
		Role string `json:"role"`
	} `json:"messages"`
}

func hasToolResult(r request) bool {
	for _, m := range r.Messages {
		if m.Role == "tool" {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, body any, headers map[string]string) {
	for k, v := range headers {
		w.Header().Set(k, v)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

type stream struct {
	w http.ResponseWriter
	f http.Flusher
}

func newStream(w http.ResponseWriter) (*stream, bool) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	f.Flush()
	return &stream{w: w, f: f}, true
}

func (s *stream) raw(line string) {
	fmt.Fprintf(s.w, "data: %s\n\n", line)
	s.f.Flush()
}

func (s *stream) chunk(payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	s.raw(string(raw))
}

func (s *stream) done() { s.raw("[DONE]") }

func delta(content string, calls []map[string]any) map[string]any {
	d := map[string]any{}
	if content != "" {
		d["content"] = content
	}
	if len(calls) > 0 {
		d["tool_calls"] = calls
	}
	return map[string]any{"choices": []map[string]any{{"delta": d}}}
}

func usageChunk() map[string]any {
	return map[string]any{"choices": []map[string]any{}, "usage": map[string]any{
		"prompt_tokens": 11, "completion_tokens": 7,
	}}
}

func finishChunk(reason string) map[string]any {
	return map[string]any{"choices": []map[string]any{{"delta": map[string]any{}, "finish_reason": reason}}}
}

func writeCall(args string) map[string]any {
	return map[string]any{
		"index": 0, "id": "call_fix",
		"type":     "function",
		"function": map[string]any{"name": "write", "arguments": args},
	}
}

func fullArgs() string {
	raw, _ := json.Marshal(map[string]string{"path": "sum.go", "content": solution})
	return string(raw)
}

// solve streams the scripted model turn: a write tool call on the first
// request, a final message once a tool result is in the conversation.
func solve(st *stream, toolResult, includeDone, includeUsage bool) {
	if toolResult {
		st.chunk(delta("Fixed Sum to iterate over every element.", nil))
		st.chunk(finishChunk("stop"))
		if includeUsage {
			st.chunk(usageChunk())
		}
		if includeDone {
			st.done()
		}
		return
	}
	st.chunk(delta("", []map[string]any{writeCall(fullArgs())}))
	st.chunk(finishChunk("tool_calls"))
	if includeUsage {
		st.chunk(usageChunk())
	}
	if includeDone {
		st.done()
	}
}

func hang(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	select {
	case <-r.Context().Done():
	case <-time.After(90 * time.Second):
	}
}

func abrupt(w http.ResponseWriter, first string) {
	st, ok := newStream(w)
	if !ok {
		return
	}
	st.raw(first)
	hij, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, buf, err := hij.Hijack()
	if err != nil {
		return
	}
	_ = buf.Flush()
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = conn.Close()
}

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "listen address")
	scenario := flag.String("scenario", "clean", "fault scenario")
	logPath := flag.String("log", "", "append one JSON line per request")
	flag.Parse()

	var logw io.Writer
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		logw = f
	}
	st := &stats{logw: logw}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"object": "list",
			"data":   []map[string]any{{"id": "fault-model"}, {"id": "good-model"}},
		}, nil)
	})
	mux.HandleFunc("/_stats", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"requests": st.count(), "scenario": *scenario}, nil)
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		var req request
		_ = json.Unmarshal(body, &req)
		tool := hasToolResult(req)
		n := st.record(map[string]any{
			"scenario": *scenario, "model": req.Model, "tool_result": tool,
			"bytes": len(body), "user_agent": r.UserAgent(),
		})

		switch *scenario {
		case "clean":
			s, ok := newStream(w)
			if !ok {
				return
			}
			solve(s, tool, true, true)
		case "flaky500":
			if n == 1 {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]string{"message": "upstream exploded"}}, nil)
				return
			}
			s, ok := newStream(w)
			if !ok {
				return
			}
			solve(s, tool, true, true)
		case "rate429":
			if n == 1 {
				writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": map[string]string{"message": "slow down"}}, map[string]string{"Retry-After": "1"})
				return
			}
			s, ok := newStream(w)
			if !ok {
				return
			}
			solve(s, tool, true, true)
		case "unauthorized":
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{"message": "bad key"}}, nil)
		case "truncate":
			if n == 1 {
				abrupt(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_fix","type":"function","function":{"name":"write","arguments":"{\"path\":\"sum.go\",\"content\":\"package fixt`)
				return
			}
			s, ok := newStream(w)
			if !ok {
				return
			}
			solve(s, tool, true, true)
		case "cleancut":
			if n == 1 {
				s, ok := newStream(w)
				if !ok {
					return
				}
				s.chunk(delta("", []map[string]any{writeCall(`{"path":"sum.go","content":"package fixture\n\nfunc Sum(nums []int) int {\n\ttotal := 0\n`)}))
				return
			}
			s, ok := newStream(w)
			if !ok {
				return
			}
			solve(s, tool, true, true)
		case "nodone":
			s, ok := newStream(w)
			if !ok {
				return
			}
			solve(s, tool, false, true)
		case "nousage":
			s, ok := newStream(w)
			if !ok {
				return
			}
			solve(s, tool, true, false)
		case "noend":
			// A complete tool call, then EOF with no finish_reason and no
			// [DONE]: the stream is truncated but ends on a value boundary.
			s, ok := newStream(w)
			if !ok {
				return
			}
			if tool {
				s.chunk(delta("Fixed Sum.", nil))
				return
			}
			s.chunk(delta("", []map[string]any{writeCall(fullArgs())}))
			return
		case "badjson":
			s, ok := newStream(w)
			if !ok {
				return
			}
			s.raw("{oops")
			solve(s, tool, true, true)
		case "badargs":
			s, ok := newStream(w)
			if !ok {
				return
			}
			if tool {
				s.chunk(delta("Could not apply the edit.", nil))
				s.chunk(finishChunk("stop"))
				s.done()
				return
			}
			s.chunk(delta("", []map[string]any{writeCall(`{"path":"sum.go","content":`)}))
			s.chunk(finishChunk("tool_calls"))
			s.done()
		case "slow":
			s, ok := newStream(w)
			if !ok {
				return
			}
			if tool {
				s.chunk(delta("Fixed", nil))
				time.Sleep(2 * time.Second)
				s.chunk(delta(" Sum.", nil))
				s.chunk(finishChunk("stop"))
				s.done()
				return
			}
			time.Sleep(2 * time.Second)
			s.chunk(delta("", []map[string]any{writeCall(fullArgs())}))
			s.chunk(finishChunk("tool_calls"))
			s.done()
		case "fallback":
			if req.Model != "good-model" {
				writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"message": "model unavailable"}}, nil)
				return
			}
			s, ok := newStream(w)
			if !ok {
				return
			}
			solve(s, tool, true, true)
		case "hang":
			hang(w, r)
		default:
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"message": "unknown scenario " + *scenario}}, nil)
		}
	})

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("LISTEN=%s\n", ln.Addr().String())
	os.Stdout.Sync()
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.Serve(ln); err != nil {
		log.Fatal(err)
	}
}
