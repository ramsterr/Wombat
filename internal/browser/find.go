package browser

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
)

var pathNames = []string{
	"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
	"chrome", "microsoft-edge", "microsoft-edge-stable", "brave-browser",
}

// FindExecutable locates a Chromium-based browser. WOMBAT_BROWSER wins, then
// PATH, then the usual application and Playwright install locations.
func FindExecutable() (string, bool) {
	if path := os.Getenv("WOMBAT_BROWSER"); path != "" {
		if isFile(path) {
			return path, true
		}
		if resolved, err := exec.LookPath(path); err == nil {
			return resolved, true
		}
		return "", false
	}
	for _, name := range pathNames {
		if path, err := exec.LookPath(name); err == nil {
			return path, true
		}
	}
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = append(candidates,
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		)
	case "windows":
		for _, root := range []string{os.Getenv("PROGRAMFILES"), os.Getenv("PROGRAMFILES(X86)"), os.Getenv("LOCALAPPDATA")} {
			if root == "" {
				continue
			}
			candidates = append(candidates,
				filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"),
				filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe"),
				filepath.Join(root, "Chromium", "Application", "chrome.exe"),
			)
		}
	}
	for _, path := range candidates {
		if isFile(path) {
			return path, true
		}
	}
	home, _ := os.UserHomeDir()
	globs := []string{
		filepath.Join(home, "Library", "Caches", "ms-playwright", "chromium-*", "chrome-mac*", "Chromium.app", "Contents", "MacOS", "Chromium"),
		filepath.Join(home, ".cache", "ms-playwright", "chromium-*", "chrome-linux*", "chrome"),
		filepath.Join(home, ".cache", "ms-playwright", "chromium_headless_shell-*", "chrome-linux*", "headless_shell"),
		filepath.Join(home, ".cache", "puppeteer", "chrome", "*", "chrome-mac*", "Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing"),
		filepath.Join(home, ".cache", "puppeteer", "chrome", "*", "chrome-linux*", "chrome"),
		filepath.Join(home, ".cache", "hyperframes", "chrome", "chrome-headless-shell", "*", "chrome-headless-shell-*", "chrome-headless-shell"),
		filepath.Join(home, ".cache", "hyperframes", "chrome", "chrome-headless-shell", "*", "chrome-headless-shell-mac-*", "chrome-headless-shell"),
		filepath.Join(home, "Library", "Caches", "hyperframes", "chrome", "chrome-headless-shell", "*", "chrome-headless-shell-*", "chrome-headless-shell"),
	}
	for _, pattern := range globs {
		matches, _ := filepath.Glob(pattern)
		sort.Strings(matches)
		for i := len(matches) - 1; i >= 0; i-- {
			if isFile(matches[i]) {
				return matches[i], true
			}
		}
	}
	return "", false
}

func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
