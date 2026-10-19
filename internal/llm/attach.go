package llm

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// MaxAttachmentBytes caps a single uploaded file. Provider request limits are
// lower for some media, but this keeps memory bounded and gives a clear error.
const MaxAttachmentBytes = 20 << 20

// maxInlineText is how much of a text-like file is folded into the prompt.
const maxInlineText = 512 << 10

type Attachment struct {
	Kind string `json:"kind"` // image, video, audio, file
	MIME string `json:"mime"`
	Name string `json:"name,omitempty"`
	Path string `json:"path"`
	Size int64  `json:"size,omitempty"`
}

var mimeByExt = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
	".tif":  "image/tiff",
	".tiff": "image/tiff",
	".heic": "image/heic",
	".svg":  "image/svg+xml",

	".mp4":  "video/mp4",
	".m4v":  "video/mp4",
	".mov":  "video/quicktime",
	".webm": "video/webm",
	".mkv":  "video/x-matroska",
	".avi":  "video/x-msvideo",

	".mp3":  "audio/mpeg",
	".wav":  "audio/wav",
	".m4a":  "audio/mp4",
	".aac":  "audio/aac",
	".ogg":  "audio/ogg",
	".flac": "audio/flac",

	".pdf":   "application/pdf",
	".txt":   "text/plain",
	".md":    "text/markdown",
	".csv":   "text/csv",
	".tsv":   "text/tab-separated-values",
	".json":  "application/json",
	".jsonl": "application/jsonl",
	".yaml":  "application/yaml",
	".yml":   "application/yaml",
	".xml":   "application/xml",
	".html":  "text/html",
	".htm":   "text/html",
	".css":   "text/css",
	".js":    "text/javascript",
	".mjs":   "text/javascript",
	".ts":    "text/plain",
	".tsx":   "text/plain",
	".jsx":   "text/plain",
	".go":    "text/plain",
	".py":    "text/plain",
	".rb":    "text/plain",
	".rs":    "text/plain",
	".java":  "text/plain",
	".c":     "text/plain",
	".h":     "text/plain",
	".cpp":   "text/plain",
	".hpp":   "text/plain",
	".sh":    "text/plain",
	".zsh":   "text/plain",
	".sql":   "text/plain",
	".toml":  "text/plain",
	".ini":   "text/plain",
	".log":   "text/plain",
	".rtf":   "application/rtf",
}

func MIMEForPath(path string) string {
	if mt, ok := mimeByExt[strings.ToLower(filepath.Ext(path))]; ok {
		return mt
	}
	f, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close()
	var buf [512]byte
	n, _ := f.Read(buf[:])
	if n == 0 {
		return "application/octet-stream"
	}
	return http.DetectContentType(buf[:n])
}

func KindFor(mime, name string) string {
	m := strings.ToLower(mime)
	switch {
	case strings.HasPrefix(m, "image/"):
		return "image"
	case strings.HasPrefix(m, "video/"):
		return "video"
	case strings.HasPrefix(m, "audio/"):
		return "audio"
	default:
		return "file"
	}
}

func NewAttachment(path string) (Attachment, error) {
	p, err := ExpandPath(path)
	if err != nil {
		return Attachment{}, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return Attachment{}, fmt.Errorf("%s: %w", path, err)
	}
	if st.IsDir() {
		return Attachment{}, fmt.Errorf("%s is a directory", path)
	}
	if st.Size() > MaxAttachmentBytes {
		return Attachment{}, fmt.Errorf("%s is %s (max %s)", filepath.Base(p), HumanSize(st.Size()), HumanSize(MaxAttachmentBytes))
	}
	mime := MIMEForPath(p)
	return Attachment{
		Kind: KindFor(mime, p),
		MIME: mime,
		Name: filepath.Base(p),
		Path: p,
		Size: st.Size(),
	}, nil
}

func LoadAttachment(a Attachment) ([]byte, error) {
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", a.Name, err)
	}
	if len(data) > MaxAttachmentBytes {
		return nil, fmt.Errorf("%s exceeds the %s attachment limit", a.Name, HumanSize(MaxAttachmentBytes))
	}
	return data, nil
}

func DataURI(mime string, data []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func IsTextMIME(mime string) bool {
	m := strings.ToLower(mime)
	return strings.HasPrefix(m, "text/") ||
		m == "application/json" || m == "application/jsonl" ||
		m == "application/xml" || m == "application/yaml"
}

func IsImageMIME(mime string) bool {
	switch strings.ToLower(mime) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}
	return false
}

func AudioFormat(mime string) (string, bool) {
	switch strings.ToLower(mime) {
	case "audio/wav", "audio/x-wav":
		return "wav", true
	case "audio/mpeg", "audio/mp3":
		return "mp3", true
	case "audio/mp4", "audio/m4a":
		return "m4a", true
	case "audio/ogg":
		return "ogg", true
	case "audio/flac":
		return "flac", true
	}
	return "", false
}

// TextAttachment returns file contents for text-like files. It reports ok=false
// for binary formats that need a provider-specific document block.
func TextAttachment(a Attachment) (text string, ok bool, err error) {
	if !IsTextMIME(a.MIME) {
		return "", false, nil
	}
	data, err := LoadAttachment(a)
	if err != nil {
		return "", false, err
	}
	if len(data) > maxInlineText {
		return string(data[:maxInlineText]) + "\n\n[file truncated to 512 KiB]", true, nil
	}
	return string(data), true, nil
}

// AppendFileText folds a text attachment into a prompt string.
func AppendFileText(base string, a Attachment, content string) string {
	header := "\n\n[attached file: " + a.Name + "]\n"
	if strings.TrimSpace(base) == "" {
		return strings.TrimLeft(header, "\n") + content
	}
	return strings.TrimRight(base, "\n") + header + content
}

func HumanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func ExpandPath(p string) (string, error) {
	p = strings.TrimSpace(strings.Trim(p, `"'`))
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return p, err
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return filepath.Abs(p)
}

// ParseMentions pulls @path tokens whose files exist out of text, returning the
// remaining text and absolute paths. Quoted paths (@"my file.png") are allowed.
func ParseMentions(text string) (string, []string) {
	var out strings.Builder
	var paths []string
	for i := 0; i < len(text); {
		if text[i] == '@' && (i == 0 || isSpaceByte(text[i-1])) {
			j := i + 1
			var raw string
			if j < len(text) && (text[j] == '"' || text[j] == '\'') {
				q := text[j]
				j++
				start := j
				for j < len(text) && text[j] != q {
					j++
				}
				raw = text[start:j]
				if j < len(text) {
					j++
				}
			} else {
				start := j
				for j < len(text) && !isSpaceByte(text[j]) {
					j++
				}
				raw = text[start:j]
			}
			if raw != "" {
				if p, err := ExpandPath(raw); err == nil {
					if st, err := os.Stat(p); err == nil && !st.IsDir() {
						paths = append(paths, p)
						i = j
						continue
					}
				}
			}
			out.WriteByte(text[i])
			i++
			continue
		}
		out.WriteByte(text[i])
		i++
	}
	result := out.String()
	if len(paths) > 0 {
		for strings.Contains(result, "  ") {
			result = strings.ReplaceAll(result, "  ", " ")
		}
	}
	return strings.TrimSpace(result), paths
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}
