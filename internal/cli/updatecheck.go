package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/release"
)

const updateCheckTTL = 24 * time.Hour

type updateCache struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

// printUpdateStatus reports whether a newer wombat release exists. It never
// fails the doctor command: a missing network is not a health problem.
func printUpdateStatus(ctx context.Context) {
	if os.Getenv("WOMBAT_NO_UPDATE_CHECK") != "" {
		fmt.Println("  " + dim("checks disabled (WOMBAT_NO_UPDATE_CHECK)"))
		return
	}
	latest, err := cachedLatest(ctx)
	if err != nil || latest == "" {
		fmt.Println("  " + dim("could not check for updates"))
		return
	}
	current := release.Normalize(Version)
	if current == "dev" {
		fmt.Println("  " + dim("development build; latest release is "+latest))
		return
	}
	if release.Compare(current, latest) >= 0 {
		fmt.Println("  " + green("✓") + " up to date " + dim("("+current+")"))
		return
	}
	fmt.Println("  " + yellow("↑") + " " + bold(latest) + dim(" available — run `wombat upgrade`"))
}

// cachedLatest returns the newest release version, cached for a day so doctor
// does not hit the GitHub API on every run.
func cachedLatest(ctx context.Context) (string, error) {
	path := filepath.Join(config.Home(), "update-check.json")
	var cached updateCache
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cached); err == nil {
			if time.Since(cached.CheckedAt) < updateCheckTTL && cached.Latest != "" {
				return cached.Latest, nil
			}
		}
	}
	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	rel, err := release.NewClient().Latest(reqCtx)
	if err != nil {
		if cached.Latest != "" {
			return cached.Latest, nil
		}
		return "", err
	}
	latest := rel.Version()
	cached = updateCache{CheckedAt: time.Now(), Latest: latest}
	if data, err := json.Marshal(cached); err == nil {
		_ = os.MkdirAll(filepath.Dir(path), 0o700)
		_ = os.WriteFile(path, data, 0o600)
	}
	return latest, nil
}
