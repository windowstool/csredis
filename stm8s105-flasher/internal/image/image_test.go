package image

import (
	"bytes"
	"testing"
)

func TestIntelHexRoundTrip(t *testing.T) {
	src := New()
	for i := 0; i < 40; i++ {
		src.Put(0x8000+uint32(i), byte(i))
	}
	src.Put(0x8100, 0xA5)
	back, err := Parse("app.hex", src.IntelHex(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if back.Len() != src.Len() {
		t.Fatalf("len %d, want %d", back.Len(), src.Len())
	}
	for a, b := range src.Data {
		if back.Data[a] != b {
			t.Fatalf("addr 0x%X = 0x%02X, want 0x%02X", a, back.Data[a], b)
		}
	}
}

func TestSRecordAndBinary(t *testing.T) {
	// S1 record: 2 address bytes, data 01 02 03, count = 2+3+1 = 6.
	// Sum of count+addr+data+csum = 0xFF.
	body := []byte{0x06, 0x80, 0x00, 0x01, 0x02, 0x03}
	var sum byte
	for _, b := range body {
		sum += b
	}
	csum := 0xFF - sum
	line := []byte(":skip\n")
	_ = line
	rec := "S1068000010203" // placeholder, replaced below
	raw := append(append([]byte{}, body...), csum)
	rec = "S1" + bytesToHex(raw) + "\nS9030000FC\n"
	img, err := Parse("a.s19", []byte(rec), 0)
	if err != nil {
		t.Fatal(err)
	}
	if img.Data[0x8000] != 1 || img.Data[0x8001] != 2 || img.Data[0x8002] != 3 || img.Len() != 3 {
		t.Fatalf("srec parsed %#v", img.Data)
	}

	bin, err := Parse("a.bin", []byte{0x11, 0x22}, 0x8000)
	if err != nil {
		t.Fatal(err)
	}
	if bin.Data[0x8000] != 0x11 || bin.Data[0x8001] != 0x22 {
		t.Fatalf("bin %#v", bin.Data)
	}
}

func TestPagesAlignToBlock(t *testing.T) {
	img := New()
	for i := 0; i < 200; i++ {
		img.Put(0x8000+uint32(i), 0xFF)
	}
	pages := Pages(img.Runs())
	if len(pages) != 2 || len(pages[0].Data) != 128 || pages[0].Addr != 0x8000 || len(pages[1].Data) != 72 || pages[1].Addr != 0x8080 {
		t.Fatalf("pages = %+v", pages)
	}
}

func TestBadHexChecksum(t *testing.T) {
	_, err := Parse("a.hex", []byte(":108000000102030405060708090A0B0C0D0E0F10FF\n"), 0)
	if err == nil {
		t.Fatal("expected checksum error")
	}
}

func bytesToHex(b []byte) string {
	const hexdigits = "0123456789ABCDEF"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexdigits[v>>4]
		out[i*2+1] = hexdigits[v&0x0F]
	}
	return string(out)
}

func TestIntelHexChecksumStable(t *testing.T) {
	img := New()
	img.Put(0x8000, 0x82)
	img.Put(0x8001, 0x00)
	text := img.IntelHex()
	if !bytes.Contains(text, []byte(":00000001FF")) {
		t.Fatalf("missing eof: %s", text)
	}
	if _, err := Parse("a.hex", text, 0); err != nil {
		t.Fatal(err)
	}
}
