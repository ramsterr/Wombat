// Package clip reads an image from the system clipboard so it can be attached
// to a message. It shells out to the platform's clipboard tools.
package clip

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

var ErrNoImage = errors.New("no image in clipboard")

// Image writes the clipboard image to a temporary PNG and returns its path.
func Image() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return macImage()
	case "linux":
		return linuxImage()
	default:
		return "", errors.New("clipboard image paste is not supported on this platform")
	}
}

func tempPNG() (string, error) {
	f, err := os.CreateTemp("", "wombat-paste-*.png")
	if err != nil {
		return "", err
	}
	path := f.Name()
	f.Close()
	return path, nil
}

func macImage() (string, error) {
	png, err := macWrite("PNGf", ".png")
	if err == nil {
		return png, nil
	}
	if !errors.Is(err, ErrNoImage) {
		return "", err
	}
	tiff, err := macWrite("TIFF", ".tiff")
	if err != nil {
		return "", err
	}
	out := strings.TrimSuffix(tiff, ".tiff") + ".png"
	if data, err := exec.Command("sips", "-s", "format", "png", tiff, "--out", out).CombinedOutput(); err != nil {
		os.Remove(tiff)
		return "", errors.New("could not convert clipboard image: " + strings.TrimSpace(string(data)))
	}
	os.Remove(tiff)
	return out, nil
}

func macWrite(class, ext string) (string, error) {
	f, err := os.CreateTemp("", "wombat-paste-*"+ext)
	if err != nil {
		return "", err
	}
	path := f.Name()
	f.Close()
	script := `on run
  set outPath to "` + strings.ReplaceAll(path, `"`, `\"`) + `"
  try
    set theData to the clipboard as «class ` + class + `»
  on error
    error number -1
  end try
  set outFile to open for access POSIX file outPath with write permission
  set eof outFile to 0
  write theData to outFile
  close access outFile
end run`
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		os.Remove(path)
		if isNoImage(out) {
			return "", ErrNoImage
		}
		return "", errors.New("could not read clipboard image: " + strings.TrimSpace(string(out)))
	}
	st, err := os.Stat(path)
	if err != nil || st.Size() == 0 {
		os.Remove(path)
		return "", ErrNoImage
	}
	return path, nil
}

func isNoImage(out []byte) bool {
	s := string(out)
	return strings.Contains(s, "-1") || strings.Contains(s, "the clipboard") || s == ""
}

func linuxImage() (string, error) {
	path, err := tempPNG()
	if err != nil {
		return "", err
	}
	var cmds [][]string
	if _, err := exec.LookPath("wl-paste"); err == nil {
		cmds = append(cmds, []string{"wl-paste", "--no-newline", "--type", "image/png"})
	}
	if _, err := exec.LookPath("xclip"); err == nil {
		cmds = append(cmds, []string{"xclip", "-selection", "clipboard", "-t", "image/png", "-o"})
	}
	for _, c := range cmds {
		out, err := exec.Command(c[0], c[1:]...).Output()
		if err == nil && len(out) > 0 {
			if err := os.WriteFile(path, out, 0o600); err != nil {
				return "", err
			}
			return path, nil
		}
	}
	os.Remove(path)
	if len(cmds) == 0 {
		return "", errors.New("install wl-paste or xclip to paste images")
	}
	return "", ErrNoImage
}
