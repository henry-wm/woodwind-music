package music

import (
	"bytes"
	"encoding/binary"
	"image"
	"testing"

	_ "golang.org/x/image/webp"
)

func makeTestAnimatedWebP(frameChunk []byte) []byte {
	// VP8X (10 bytes): animation flag (0x02), 1x1 canvas (w-1=0, h-1=0).
	vp8x := []byte{'V', 'P', '8', 'X', 10, 0, 0, 0, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	// ANIM (6 bytes): bg color + loop count.
	anim := []byte{'A', 'N', 'I', 'M', 6, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	// ANMF (16-byte header + frameChunk).
	anmfBody := make([]byte, 16, 16+len(frameChunk))
	anmfBody = append(anmfBody, frameChunk...)
	anmf := append([]byte("ANMF"), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(anmf[4:8], uint32(len(anmfBody)))
	anmf = append(anmf, anmfBody...)
	if len(anmfBody)%2 == 1 {
		anmf = append(anmf, 0)
	}
	body := append([]byte("WEBP"), vp8x...)
	body = append(body, anim...)
	body = append(body, anmf...)
	out := binary.LittleEndian.AppendUint32([]byte("RIFF"), uint32(len(body)))
	return append(out, body...)
}

func TestStillCoverExtractsAnimatedWebPFirstFrame(t *testing.T) {
	// Valid 1x1 VP8 still WebP.
	realStill := []byte{
		0x52, 0x49, 0x46, 0x46, 0x24, 0x00, 0x00, 0x00,
		0x57, 0x45, 0x42, 0x50, 0x56, 0x50, 0x38, 0x20,
		0x18, 0x00, 0x00, 0x00, 0x30, 0x01, 0x00, 0x9d,
		0x01, 0x2a, 0x01, 0x00, 0x01, 0x00, 0x02, 0x00,
		0x34, 0x25, 0xa4, 0x00, 0x03, 0x70, 0x00, 0xfe,
		0xfb, 0xfd, 0x50, 0x00,
	}
	if _, _, err := image.Decode(bytes.NewReader(realStill)); err != nil {
		t.Fatalf("test still VP8 WebP did not decode: %v", err)
	}

	vp8Chunk := realStill[12:]
	animated := makeTestAnimatedWebP(vp8Chunk)

	// Confirm standard x/image/webp fails on animated WebP directly.
	if _, _, err := image.Decode(bytes.NewReader(animated)); err == nil {
		t.Fatal("expected animated WebP to fail direct image.Decode")
	}

	extracted := StillCover(animated)
	img, format, err := image.Decode(bytes.NewReader(extracted))
	if err != nil {
		t.Fatalf("StillCover output failed to decode: %v", err)
	}
	if format != "webp" {
		t.Fatalf("format = %q, want webp", format)
	}
	if b := img.Bounds(); b.Dx() != 1 || b.Dy() != 1 {
		t.Fatalf("bounds = %v, want 1x1", b)
	}

	// Non-animated data passes through unchanged.
	if !bytes.Equal(StillCover(realStill), realStill) {
		t.Fatal("still WebP should pass through unchanged")
	}
	plain := []byte("not-a-webp")
	if !bytes.Equal(StillCover(plain), plain) {
		t.Fatal("non-WebP data should pass through unchanged")
	}
}
