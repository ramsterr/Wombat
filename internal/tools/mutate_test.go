package tools

import "testing"

func TestMayMutateIgnoresHarmlessRedirects(t *testing.T) {
	harmless := []string{
		"pwd; ls -la; which ffmpeg ffprobe python3 node convert magick 2>/dev/null; sw_vers",
		"python3 -c \"import PIL, numpy; print(PIL.__version__)\" 2>&1 | head -3",
		"ls -la",
		"echo hi",
		"git status",
		"grep -r foo . 2>/dev/null",
		"curl https://example.com",
		"yt-dlp --flat-playlist --print \"%(id)s\" \"ytsearch5:test\"",
		"ffmpeg -version",
	}
	for _, cmd := range harmless {
		if MayMutate(cmd) {
			t.Errorf("MayMutate(%q) = true, want false", cmd)
		}
	}
	mutating := []string{
		"echo hi > out.txt",
		"echo hi >> out.txt",
		"cat a > b",
		"sed -i 's/a/b/' f.txt",
		"rm -rf build",
		"mkdir -p x",
		"cp a b",
		"curl -o out.png https://example.com/a.png",
		"wget -O out.png https://example.com/a.png",
		"yt-dlp https://example.com/video",
		"ffmpeg -i in.mp4 out.mp4",
		"python3 -c \"open('x','w').write('a')\"",
	}
	for _, cmd := range mutating {
		if !MayMutate(cmd) {
			t.Errorf("MayMutate(%q) = false, want true", cmd)
		}
	}
}
