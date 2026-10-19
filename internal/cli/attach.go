package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
)

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ", ") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func attachFiles(sess *session.Session, paths []string, quiet bool) ([]llm.Attachment, error) {
	out := make([]llm.Attachment, 0, len(paths))
	for _, p := range paths {
		a, err := sess.StoreAttachment(p)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
		if !quiet {
			fmt.Fprintln(os.Stderr, dim("attached "+a.Name+" ("+a.Kind+", "+llm.HumanSize(a.Size)+")"))
		}
	}
	return out, nil
}
