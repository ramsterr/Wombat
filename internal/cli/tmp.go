package cli

import (
	"os"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/session"
)

// tmpDir returns the scratch directory for a session and makes sure it
// exists. It sits outside the project, so scratch files never pollute the
// repository and never end up in a checkpoint.
func tmpDir(sess *session.Session) string {
	id := ""
	if sess != nil {
		id = sess.ID()
	}
	dir := config.TmpDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	return dir
}

// tmpNote tells the model where to put temporary files.
func tmpNote(sess *session.Session) string {
	dir := tmpDir(sess)
	if dir == "" {
		return ""
	}
	return "Temporary files: put scratch output in " + dir +
		" (the environment variable WOMBAT_TMP points there). It is outside the project, " +
		"so it never pollutes the repository and is not part of any checkpoint."
}
