package lsp

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

func URI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	abs = filepath.ToSlash(abs)
	if runtime.GOOS == "windows" && !strings.HasPrefix(abs, "/") {
		abs = "/" + abs
	}
	return (&url.URL{Scheme: "file", Path: abs}).String()
}

func Path(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	if u.Scheme != "file" {
		return "", &url.Error{Op: "open", URL: uri, Err: errUnsupportedScheme{scheme: u.Scheme}}
	}
	path := u.Path
	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.FromSlash(path), nil
}

type errUnsupportedScheme struct{ scheme string }

func (e errUnsupportedScheme) Error() string {
	return "unsupported URI scheme " + e.scheme
}
