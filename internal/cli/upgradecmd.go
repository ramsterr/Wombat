package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ramsterr/Wombat/internal/release"
)

const upgradeTimeout = 5 * time.Minute

func cmdUpgrade(args []string) error {
	var (
		checkOnly bool
		method    string
		target    string
	)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--check", "-c":
			checkOnly = true
		case "--method", "-m":
			if i+1 >= len(args) {
				return fmt.Errorf("--method needs a value (auto, script, go, brew)")
			}
			i++
			method = strings.ToLower(args[i])
		case "--version", "-v":
			if i+1 >= len(args) {
				return fmt.Errorf("--version needs a value")
			}
			i++
			target = args[i]
		case "--help", "-h":
			fmt.Println("usage: wombat upgrade [--check] [--method auto|script|go|brew] [--version <tag>]")
			return nil
		default:
			return fmt.Errorf("unknown argument %q", args[i])
		}
	}
	if method == "" {
		method = detectInstallMethod()
	}
	if method != "auto" && method != "script" && method != "go" && method != "brew" {
		return fmt.Errorf("unknown method %q (auto, script, go, brew)", method)
	}

	ctx, cancel := context.WithTimeout(context.Background(), upgradeTimeout)
	defer cancel()

	client := release.NewClient()
	var rel release.Release
	var err error
	if target != "" {
		rel, err = client.Release(ctx, target)
	} else {
		rel, err = client.Latest(ctx)
	}
	if err != nil {
		return fmt.Errorf("could not check for updates: %w", err)
	}
	latest := rel.Version()
	current := release.Normalize(Version)
	if current != "dev" && release.Compare(current, latest) >= 0 {
		fmt.Println(green("wombat is up to date") + dim(" ("+current+")"))
		return nil
	}
	if current == "dev" {
		fmt.Println(yellow("this is a development build") + dim(" (version "+current+")"))
	}
	fmt.Printf("%s %s %s\n", bold("update available"), bold(current), dim("→ "+latest))
	if checkOnly {
		fmt.Println(dim("run `wombat upgrade` to install it"))
		return nil
	}
	switch method {
	case "brew":
		return upgradeWithBrew(ctx, latest)
	case "go":
		return upgradeWithGo(ctx, latest)
	default:
		return upgradeInPlace(ctx, client, rel, method)
	}
}

// detectInstallMethod guesses how this binary was installed.
func detectInstallMethod() string {
	exe, err := os.Executable()
	if err != nil {
		return "script"
	}
	return installMethodFor(exe)
}

// installMethodFor maps an executable path to the way it was installed.
func installMethodFor(exe string) string {
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	lower := strings.ToLower(exe)
	switch {
	case strings.Contains(lower, "/cellar/"), strings.Contains(lower, "/homebrew/"), strings.Contains(lower, "/linuxbrew/"):
		return "brew"
	case strings.Contains(lower, "/go/bin/"), strings.Contains(lower, "/gobin/"):
		return "go"
	}
	return "script"
}

func upgradeWithBrew(ctx context.Context, version string) error {
	// The fully qualified name taps the repository on demand.
	formula := "ramsterr/wombat/wombat"
	if _, err := exec.LookPath("brew"); err != nil {
		return fmt.Errorf("homebrew is not installed; install with `brew install %s`", formula)
	}
	fmt.Println(dim("running: brew upgrade " + formula))
	cmd := exec.CommandContext(ctx, "brew", "upgrade", formula)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("brew upgrade failed: %w", err)
	}
	fmt.Println(green("upgraded to ") + version)
	return nil
}

func upgradeWithGo(ctx context.Context, version string) error {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return fmt.Errorf("go is not installed; reinstall with `go install github.com/ramsterr/Wombat/cmd/wombat@latest`")
	}
	mod := "github.com/ramsterr/Wombat/cmd/wombat"
	rev := "latest"
	if version != "" {
		rev = "v" + version
	}
	fmt.Println(dim("running: go install " + mod + "@" + rev))
	cmd := exec.CommandContext(ctx, goBin, "install", mod+"@"+rev)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go install failed: %w", err)
	}
	fmt.Println(green("upgraded to ") + version)
	return nil
}

// upgradeInPlace downloads the release archive for this platform, verifies its
// checksum, and replaces the running binary.
func upgradeInPlace(ctx context.Context, client *release.Client, rel release.Release, method string) error {
	asset, ok := rel.Asset(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return fmt.Errorf("no release asset for %s/%s in %s", runtime.GOOS, runtime.GOARCH, rel.Tag)
	}
	sumAsset, ok := rel.Checksums()
	if !ok {
		return fmt.Errorf("release %s has no %s", rel.Tag, release.ChecksumName)
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot locate the running binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if err := upgradeBinary(ctx, rel, asset, sumAsset, exe); err != nil {
		return err
	}
	fmt.Println(green("upgraded to ") + rel.Version() + dim(" ("+exe+")"))
	if method == "script" {
		fmt.Println(dim("future updates: re-run the install script or `wombat upgrade`"))
	}
	return nil
}

// upgradeBinary downloads, verifies, and installs a release over target.
func upgradeBinary(ctx context.Context, rel release.Release, asset, sumAsset release.Asset, target string) error {
	dir, err := os.MkdirTemp("", "wombat-upgrade-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	fmt.Println(dim("downloading " + asset.Name))
	archive, err := release.Download(ctx, asset, dir)
	if err != nil {
		return err
	}
	sums, err := release.Download(ctx, sumAsset, dir)
	if err != nil {
		return err
	}
	manifest, err := os.ReadFile(sums)
	if err != nil {
		return err
	}
	if err := release.VerifyChecksum(archive, manifest); err != nil {
		return err
	}
	binary, err := extractBinary(archive, asset.Name)
	if err != nil {
		return err
	}
	return replaceBinary(target, binary)
}

// extractBinary pulls the wombat executable out of a release archive.
func extractBinary(archive, name string) (string, error) {
	dir, err := os.MkdirTemp("", "wombat-extract-")
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(name, ".zip") {
		if err := extractZip(archive, dir); err != nil {
			return "", err
		}
	} else if err := extractTarGz(archive, dir); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "wombat")
	if runtime.GOOS == "windows" {
		path = filepath.Join(dir, "wombat.exe")
	}
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("archive %s does not contain the wombat binary", name)
	}
	return path, nil
}

func extractTarGz(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.Base(hdr.Name)
		if name != "wombat" && name != "wombat.exe" {
			continue
		}
		out, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		out.Close()
	}
}

func extractZip(archive, dir string) error {
	data, err := os.ReadFile(archive)
	if err != nil {
		return err
	}
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range r.File {
		name := filepath.Base(f.Name)
		if name != "wombat" && name != "wombat.exe" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// replaceBinary swaps the running executable for a new build.
func replaceBinary(target, source string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".wombat-upgrade-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("cannot replace %s: %w", target, err)
	}
	return nil
}
