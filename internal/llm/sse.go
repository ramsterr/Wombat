package llm

import (
	"bufio"
	"io"
	"strings"
)

func readSSE(r io.Reader, fn func(event, data string) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	event := ""
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			event = ""
			continue
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(line[len("event:"):])
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(line[len("data:"):])
			if err := fn(event, data); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}

func readNDJSON(r io.Reader, fn func(line []byte) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if err := fn([]byte(line)); err != nil {
			return err
		}
	}
	return sc.Err()
}
