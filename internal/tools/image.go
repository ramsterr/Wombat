package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ramsterr/Wombat/internal/images"
)

// ImageGen creates or edits an image through an image API and attaches the
// result to the conversation, so UI work is not limited to hand-drawn assets.
type ImageGen struct{}

type imageArgs struct {
	Action  string `json:"action"`
	Prompt  string `json:"prompt"`
	Path    string `json:"path"`
	Image   string `json:"image"`
	Mask    string `json:"mask"`
	Size    string `json:"size"`
	Quality string `json:"quality"`
}

func (t *ImageGen) Name() string  { return "imagegen" }
func (t *ImageGen) Class() string { return "write" }
func (t *ImageGen) Description() string {
	return "Generate or edit an image with an image model, save it in the workspace, and attach it so you can look at it. Use this for icons, textures, illustrations, and UI assets instead of placeholders. Edit takes an input image and an optional mask."
}
func (t *ImageGen) Schema() map[string]any {
	return obj(map[string]any{
		"action":  map[string]any{"type": "string", "enum": []string{"generate", "edit"}, "description": "generate a new image (default) or edit an existing one"},
		"prompt":  desc("what the image should contain, including style, palette, and composition"),
		"path":    desc("output file path, relative to the working directory"),
		"image":   desc("input image path for action=edit"),
		"mask":    desc("optional transparent mask path for action=edit"),
		"size":    desc("image size such as 1024x1024; provider support varies"),
		"quality": desc("provider quality such as low, medium, high, standard, or hd"),
	}, "prompt", "path")
}

func (t *ImageGen) Run(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[imageArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	action := strings.ToLower(strings.TrimSpace(a.Action))
	if action == "" {
		action = "generate"
	}
	if action != "generate" && action != "edit" {
		return "", fmt.Errorf("action must be generate or edit")
	}
	if strings.TrimSpace(a.Prompt) == "" {
		return "", fmt.Errorf("prompt is required")
	}
	if strings.TrimSpace(a.Path) == "" {
		return "", fmt.Errorf("path is required")
	}
	if env == nil || env.Images == nil {
		return "", fmt.Errorf("image generation is not configured: set image_provider and image_model in the config, or add an image API key")
	}
	path, err := env.ResolvePath(a.Path)
	if err != nil {
		return "", err
	}
	if err := env.CheckPath("write", a.Path); err != nil {
		return "", err
	}
	if err := env.Check("network", "image generation"); err != nil {
		return "", err
	}
	runCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	opts := images.Options{Size: strings.TrimSpace(a.Size), Quality: strings.TrimSpace(a.Quality)}
	var result *images.Result
	if action == "edit" {
		if strings.TrimSpace(a.Image) == "" {
			return "", fmt.Errorf("image is required for action=edit")
		}
		if err := env.CheckPath("read", a.Image); err != nil {
			return "", err
		}
		inputPath, err := env.ResolvePath(a.Image)
		if err != nil {
			return "", err
		}
		input, err := os.ReadFile(inputPath)
		if err != nil {
			return "", err
		}
		if len(input) > maxImageBytes {
			return "", fmt.Errorf("input image is too large (%d bytes)", len(input))
		}
		var mask []byte
		maskName := ""
		if strings.TrimSpace(a.Mask) != "" {
			if err := env.CheckPath("read", a.Mask); err != nil {
				return "", err
			}
			maskPath, err := env.ResolvePath(a.Mask)
			if err != nil {
				return "", err
			}
			mask, err = os.ReadFile(maskPath)
			if err != nil {
				return "", err
			}
			maskName = filepath.Base(maskPath)
		}
		result, err = env.Images.Edit(runCtx, a.Prompt, input, filepath.Base(inputPath), mask, maskName, opts)
		if err != nil {
			return "", err
		}
	} else {
		result, err = env.Images.Generate(runCtx, a.Prompt, opts)
		if err != nil {
			return "", err
		}
	}
	if ext := imageExt(result.MIME); filepath.Ext(path) == "" {
		path += ext
	}
	if err := env.Snapshot("before image generation", a.Path); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, result.Data, 0o644); err != nil {
		return "", err
	}
	rel, err := filepath.Rel(env.Dir, path)
	if err != nil {
		rel = path
	}
	env.AddMedia(Media{Path: path, MIME: result.MIME, Name: filepath.Base(path)})
	verb := "generated"
	if action == "edit" {
		verb = "edited"
	}
	return fmt.Sprintf("%s image %s (%s, %d bytes); attached for you to look at", verb, filepath.ToSlash(rel), result.MIME, len(result.Data)), nil
}

func imageExt(mime string) string {
	switch strings.ToLower(mime) {
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}
