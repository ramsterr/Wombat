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
	}
	for _, cmd := range mutating {
		if !MayMutate(cmd) {
			t.Errorf("MayMutate(%q) = false, want true", cmd)
		}
	}
}
