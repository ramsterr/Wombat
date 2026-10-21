// Package images talks to an OpenAI-compatible image API. It is deliberately
// separate from the chat providers: image endpoints use their own model and
// response format.
package images

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

const maxDownloadBytes = 32 << 20

type Config struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

type Client struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

type Options struct {
	Size    string
	Quality string
	N       int
}

type Result struct {
	Data []byte
	MIME string
}

func New(cfg Config) *Client {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = "gpt-image-1"
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	return &Client{baseURL: base, apiKey: strings.TrimSpace(cfg.APIKey), model: model, http: client}
}

func (c *Client) Model() string { return c.model }

func (c *Client) Generate(ctx context.Context, prompt string, opts Options) (*Result, error) {
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("prompt is required")
	}
	body := imageRequest{
		Model:   c.model,
		Prompt:  prompt,
		Size:    opts.Size,
		Quality: opts.Quality,
		N:       opts.N,
	}
	var payload bytes.Buffer
	if err := json.NewEncoder(&payload).Encode(body); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/images/generations", &payload)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req)
	return c.decodeResponse(req)
}

func (c *Client) Edit(ctx context.Context, prompt string, image []byte, filename string, mask []byte, maskName string, opts Options) (*Result, error) {
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("prompt is required")
	}
	if len(image) == 0 {
		return nil, errors.New("an input image is required")
	}
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	if err := writer.WriteField("model", c.model); err != nil {
		return nil, err
	}
	if err := writer.WriteField("prompt", prompt); err != nil {
		return nil, err
	}
	if opts.Size != "" {
		if err := writer.WriteField("size", opts.Size); err != nil {
			return nil, err
		}
	}
	if opts.Quality != "" {
		if err := writer.WriteField("quality", opts.Quality); err != nil {
			return nil, err
		}
	}
	if opts.N > 0 {
		if err := writer.WriteField("n", fmt.Sprint(opts.N)); err != nil {
			return nil, err
		}
	}
	if err := writeFile(writer, "image", filename, image); err != nil {
		return nil, err
	}
	if len(mask) > 0 {
		if err := writeFile(writer, "mask", maskName, mask); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/images/edits", &payload)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	c.authorize(req)
	return c.decodeResponse(req)
}

type imageRequest struct {
	Model   string `json:"model"`
	Prompt  string `json:"prompt"`
	Size    string `json:"size,omitempty"`
	Quality string `json:"quality,omitempty"`
	N       int    `json:"n,omitempty"`
}

type imageResponse struct {
	Data []struct {
		B64JSON string `json:"b64_json"`
		URL     string `json:"url"`
	} `json:"data"`
}

func (c *Client) decodeResponse(req *http.Request) (*Result, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("image request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, responseError(resp)
	}
	var decoded imageResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode image response: %w", err)
	}
	if len(decoded.Data) == 0 {
		return nil, errors.New("image response contained no images")
	}
	item := decoded.Data[0]
	if item.B64JSON != "" {
		data, err := base64.StdEncoding.DecodeString(item.B64JSON)
		if err != nil {
			return nil, fmt.Errorf("decode image data: %w", err)
		}
		return result(data)
	}
	if item.URL != "" {
		return c.download(req.Context(), item.URL)
	}
	return nil, errors.New("image response contained neither b64_json nor url")
}

func (c *Client) download(ctx context.Context, target string) (*Result, error) {
	if strings.HasPrefix(target, "data:") {
		comma := strings.IndexByte(target, ',')
		if comma < 0 {
			return nil, errors.New("invalid data image URL")
		}
		data, err := base64.StdEncoding.DecodeString(target[comma+1:])
		if err != nil {
			return nil, err
		}
		return result(data)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download generated image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download generated image: http %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDownloadBytes {
		return nil, fmt.Errorf("generated image is larger than %d bytes", maxDownloadBytes)
	}
	return result(data)
}

func result(data []byte) (*Result, error) {
	if len(data) == 0 {
		return nil, errors.New("generated image was empty")
	}
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		mime = "image/png"
	}
	return &Result{Data: data, MIME: mime}, nil
}

func (c *Client) authorize(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}

func writeFile(writer *multipart.Writer, field, name string, data []byte) error {
	if name == "" {
		name = "image.png"
	}
	part, err := writer.CreateFormFile(field, name)
	if err != nil {
		return err
	}
	_, err = part.Write(data)
	return err
}

func responseError(resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	message := strings.TrimSpace(string(data))
	var decoded struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &decoded) == nil && decoded.Error.Message != "" {
		message = decoded.Error.Message
	}
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return fmt.Errorf("image API returned http %d: %s", resp.StatusCode, message)
}
