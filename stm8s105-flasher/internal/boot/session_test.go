package boot

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"stm8s105-flasher/internal/chip"
	"stm8s105-flasher/internal/image"
	"stm8s105-flasher/internal/port"
)

type pipePort struct{ net.Conn }

func (pipePort) SetDTR(bool) error { return nil }
func (pipePort) SetRTS(bool) error { return nil }
func (pipePort) Flush() error      { return nil }

func TestProgramK4AgainstFakeBootloader(t *testing.T) {
	client, server := net.Pipe()
	chip := newFake(0x13, 16*1024)
	errc := make(chan error, 1)
	go func() { errc <- chip.serve(server) }()

	s := NewSession(pipePort{client}, func(string) {})
	if err := s.Sync(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	info, model, err := Prepare(s)
	if err != nil {
		t.Fatal(err)
	}
	if info.String() != "1.3" {
		t.Fatalf("version %s", info.String())
	}
	if model.ID != chipK4 {
		t.Fatalf("model %s", model.ID)
	}

	fw := image.New()
	for i := 0; i < 200; i++ {
		fw.Put(0x8000+uint32(i), byte(i+1))
	}
	fw.Put(0x9000, 0x5A)
	if err := Program(s, *model, fw, true, true, false, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		addr := uint32(0x8000 + i)
		if chip.mem[addr] != byte(i+1) {
			t.Fatalf("flash 0x%X = 0x%02X", addr, chip.mem[addr])
		}
	}
	if chip.mem[0x9000] != 0x5A {
		t.Fatalf("gap byte 0x%02X", chip.mem[0x9000])
	}
	if chip.mem[0x487E] != 0x55 || chip.mem[0x487F] != 0xAA {
		t.Fatalf("option bytes %02X %02X", chip.mem[0x487E], chip.mem[0x487F])
	}
	if _, ok := chip.mem[0x00A0]; !ok {
		t.Fatal("RAM routine was not uploaded")
	}
	if len(chip.erased) != 1 {
		t.Fatalf("erase calls %d", len(chip.erased))
	}
	if !contains(chip.erased[0], 0x00) || !contains(chip.erased[0], 0x04) {
		t.Fatalf("sectors %v", chip.erased[0])
	}
	if contains(chip.erased[0], chipEEPROM) {
		t.Fatal("EEPROM sector was erased")
	}

	client.Close()
	select {
	case <-errc:
	case <-time.After(2 * time.Second):
		t.Fatal("fake bootloader did not exit")
	}
}

func TestEraseWholeK4Flash(t *testing.T) {
	client, server := net.Pipe()
	dev := newFake(0x13, 16*1024)
	go dev.serve(server)
	s := NewSession(pipePort{client}, func(string) {})
	if err := s.Sync(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	_, model, err := Prepare(s)
	if err != nil {
		t.Fatal(err)
	}
	dev.mem[0x8000] = 0x82
	dev.mem[0xBFFF] = 0x11
	if err := EraseFlash(s, *model, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := dev.mem[0x8000]; ok {
		t.Fatal("flash byte survived erase")
	}
	if _, ok := dev.mem[0xBFFF]; ok {
		t.Fatal("last flash byte survived erase")
	}
	if len(dev.erased) != 1 || len(dev.erased[0]) != 16 {
		t.Fatalf("sectors %+v", dev.erased)
	}
	client.Close()
}

const (
	chipK4     = "STM8S105K4"
	chipEEPROM = 0x20
)

func contains(b []byte, v byte) bool {
	for _, x := range b {
		if x == v {
			return true
		}
	}
	return false
}

type fakeDev struct {
	mem     map[uint32]byte
	flash   int
	version byte
	erased  [][]byte
}

func newFake(version byte, flash int) *fakeDev {
	return &fakeDev{mem: map[uint32]byte{}, flash: flash, version: version}
}

func (f *fakeDev) serve(conn net.Conn) error {
	defer conn.Close()
	b, err := readb(conn)
	if err != nil {
		return err
	}
	if b != syncByte {
		return errors.New("expected sync")
	}
	if err := echo(conn, ackByte); err != nil {
		return err
	}
	for {
		cmd, err := readb(conn)
		if err != nil {
			return nil
		}
		comp, err := readb(conn)
		if err != nil {
			return err
		}
		if comp != cmd^0xFF {
			if err := echo(conn, nackByte); err != nil {
				return err
			}
			continue
		}
		if err := echo(conn, ackByte); err != nil {
			return err
		}
		switch cmd {
		case cmdGet:
			payload := []byte{0x05, f.version, cmdGet, cmdRead, cmdGo, cmdWrite, cmdErase, ackByte}
			for _, b := range payload {
				if err := echo(conn, b); err != nil {
					return err
				}
			}
		case cmdRead:
			if err := f.onRead(conn); err != nil {
				return err
			}
		case cmdWrite:
			if err := f.onWrite(conn); err != nil {
				return err
			}
		case cmdErase:
			if err := f.onErase(conn); err != nil {
				return err
			}
		case cmdGo:
			if _, err := readn(conn, 5); err != nil {
				return err
			}
			return echo(conn, ackByte)
		default:
			if err := echo(conn, nackByte); err != nil {
				return err
			}
		}
	}
}

func (f *fakeDev) onRead(conn net.Conn) error {
	addr, ok, err := readAddr(conn)
	if err != nil {
		return err
	}
	if !ok || !f.valid(addr) {
		return echo(conn, nackByte)
	}
	if err := echo(conn, ackByte); err != nil {
		return err
	}
	hdr, err := readn(conn, 2)
	if err != nil {
		return err
	}
	if hdr[1] != hdr[0]^0xFF {
		return echo(conn, nackByte)
	}
	n := int(hdr[0]) + 1
	if !f.valid(addr + uint32(n) - 1) {
		return echo(conn, nackByte)
	}
	if err := echo(conn, ackByte); err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		if err := echo(conn, f.mem[addr+uint32(i)]); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeDev) onWrite(conn net.Conn) error {
	addr, ok, err := readAddr(conn)
	if err != nil {
		return err
	}
	if !ok || !f.valid(addr) {
		return echo(conn, nackByte)
	}
	if err := echo(conn, ackByte); err != nil {
		return err
	}
	count, err := readb(conn)
	if err != nil {
		return err
	}
	data, err := readn(conn, int(count)+1)
	if err != nil {
		return err
	}
	chk, err := readb(conn)
	if err != nil {
		return err
	}
	sum := count
	for _, b := range data {
		sum ^= b
	}
	if chk != sum || !f.valid(addr+uint32(len(data))-1) {
		return echo(conn, nackByte)
	}
	for i, b := range data {
		f.mem[addr+uint32(i)] = b
	}
	return echo(conn, ackByte)
}

func (f *fakeDev) onErase(conn net.Conn) error {
	count, err := readb(conn)
	if err != nil {
		return err
	}
	if count == 0xFF {
		zero, err := readb(conn)
		if err != nil {
			return err
		}
		if zero != 0x00 {
			return echo(conn, nackByte)
		}
		f.wipeFlash()
		return echo(conn, ackByte)
	}
	secs, err := readn(conn, int(count)+1)
	if err != nil {
		return err
	}
	chk, err := readb(conn)
	if err != nil {
		return err
	}
	sum := count
	for _, s := range secs {
		sum ^= s
	}
	if chk != sum {
		return echo(conn, nackByte)
	}
	f.erased = append(f.erased, append([]byte(nil), secs...))
	for _, sec := range secs {
		f.wipeSector(sec)
	}
	return echo(conn, ackByte)
}

func (f *fakeDev) wipeFlash() {
	for i := 0; i < f.flash; i += chip.SectorSize {
		f.wipeSector(byte(i / chip.SectorSize))
	}
}

func (f *fakeDev) wipeSector(sec byte) {
	var start uint32
	switch {
	case sec == chip.EEPROMSector:
		start = 0x4000
	default:
		start = 0x8000 + uint32(sec)*chip.SectorSize
	}
	for a := start; a < start+chip.SectorSize; a++ {
		delete(f.mem, a)
	}
}

func (f *fakeDev) valid(addr uint32) bool {
	switch {
	case addr < 0x400:
		return true
	case addr >= 0x4000 && addr < 0x4400:
		return true
	case addr >= 0x4800 && addr < 0x4800+chip.OptionBlock:
		return true
	case addr >= 0x8000 && addr < 0x8000+uint32(f.flash):
		return true
	default:
		return false
	}
}

func readAddr(conn net.Conn) (uint32, bool, error) {
	raw, err := readn(conn, 5)
	if err != nil {
		return 0, false, err
	}
	if raw[4] != raw[0]^raw[1]^raw[2]^raw[3] {
		return 0, false, nil
	}
	addr := uint32(raw[0])<<24 | uint32(raw[1])<<16 | uint32(raw[2])<<8 | uint32(raw[3])
	return addr, true, nil
}

func echo(conn net.Conn, b byte) error {
	if _, err := conn.Write([]byte{b}); err != nil {
		return err
	}
	got, err := readb(conn)
	if err != nil {
		return err
	}
	if got != b {
		return errors.New("reply-mode echo mismatch")
	}
	return nil
}

func readb(r io.Reader) (byte, error) {
	buf, err := readn(r, 1)
	if err != nil {
		return 0, err
	}
	return buf[0], nil
}

func readn(r io.Reader, n int) ([]byte, error) {
	buf := make([]byte, n)
	_, err := io.ReadFull(r, buf)
	return buf, err
}

// Ensure the test double satisfies the port used by Session.
var _ port.Port = pipePort{}
