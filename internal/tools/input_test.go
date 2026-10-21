package tools

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadRejectsBinary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blob.bin"), []byte{0x00, 0x01, 0x02, 0x00, 0xff}, 0o644); err != nil {
		t.Fatal(err)
	}
	env := testEnv(dir)
	err := runErr(t, &Read{}, env, `{"path":"blob.bin"}`)
	if !strings.Contains(err.Error(), "binary") {
		t.Fatalf("error = %v", err)
	}
}

func TestReadLargeImageIsDownscaled(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 2400, 1200))
	for y := 0; y < 1200; y++ {
		for x := 0; x < 2400; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x % 255), uint8(y % 255), 120, 255})
		}
	}
	path := filepath.Join(dir, "big.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	env := testEnv(dir)
	var media []Media
	env.SetMediaSink(&media)
	run(t, &Read{}, env, `{"path":"big.png"}`)
	if len(media) != 1 {
		t.Fatalf("media = %+v", media)
	}
	if media[0].Note == "" || !strings.Contains(media[0].Note, "downscaled") {
		t.Fatalf("note = %q", media[0].Note)
	}
	f2, err := os.Open(media[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()
	cfg, _, err := image.DecodeConfig(f2)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width > maxImageDimension || cfg.Height > maxImageDimension {
		t.Fatalf("scaled size = %dx%d", cfg.Width, cfg.Height)
	}
}

func TestConcurrentMediaAdds(t *testing.T) {
	var media []Media
	env := &Env{Dir: t.TempDir()}
	env.SetMediaSink(&media)
	done := make(chan struct{})
	for i := 0; i < 20; i++ {
		go func() {
			env.AddMedia(Media{Name: "x"})
			done <- struct{}{}
		}()
	}
	for i := 0; i < 20; i++ {
		<-done
	}
	if len(media) != 20 {
		t.Fatalf("media = %d", len(media))
	}
}

func TestVerifyHonorsBashPermission(t *testing.T) {
	dir := t.TempDir()
	env := &Env{Dir: dir, Permit: func(kind, detail string) Decision { return Decision{} }}
	if _, err := (&Verify{}).Run(context.Background(), json.RawMessage(`{"command":"touch made.txt"}`), env); err == nil {
		t.Fatal("verify should not run a denied command")
	}
	if _, err := os.Stat(filepath.Join(dir, "made.txt")); err == nil {
		t.Fatal("denied verify command still ran")
	}
}
