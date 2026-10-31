// Package boot speaks the STM8S105 UART2 reply-mode ROM bootloader protocol
// described in ST UM0560.
package boot

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"stm8s105-flasher/internal/image"
	"stm8s105-flasher/internal/port"
)

const (
	ackByte   = 0x79
	nackByte  = 0x1F
	syncByte  = 0x7F
	cmdGet    = 0x00
	cmdRead   = 0x11
	cmdGo     = 0x21
	cmdWrite  = 0x31
	cmdErase  = 0x43
	readChunk = 128
)

// Info is what the GET command reports.
type Info struct {
	Version  byte
	Commands []byte
}

// String renders the BCD version, for example 0x13 as "1.3".
func (i Info) String() string {
	return fmt.Sprintf("%d.%d", i.Version>>4, i.Version&0x0F)
}

// Session is one bootloader conversation on an open port.
type Session struct {
	port port.Port
	log  func(string)
}

// NewSession wraps an already open 8N1 port.
func NewSession(p port.Port, log func(string)) *Session {
	if log == nil {
		log = func(string) {}
	}
	return &Session{port: p, log: log}
}

// Sync sends 0x7F until the chip answers. window is how long to keep trying
// while the operator presses reset. The chip's own window is about one second
// after reset when flash already contains an application.
func (s *Session) Sync(window time.Duration) error {
	if window <= 0 {
		window = 8 * time.Second
	}
	_ = s.port.Flush()
	deadline := time.Now().Add(window)
	var last error
	for time.Now().Before(deadline) {
		if err := writeFull(s.port, []byte{syncByte}); err != nil {
			return err
		}
		b, err := s.readRaw(1, 150*time.Millisecond)
		if err != nil {
			last = err
			continue
		}
		// A 1-wire adapter loops the sync byte back before the real answer.
		if b[0] == syncByte {
			b, err = s.readRaw(1, 250*time.Millisecond)
			if err != nil {
				last = err
				continue
			}
		}
		if b[0] != ackByte && b[0] != nackByte {
			last = fmt.Errorf("收到 0x%02X", b[0])
			continue
		}
		// Reply mode waits until the host echoes the byte the chip just sent.
		if err := writeFull(s.port, []byte{b[0]}); err != nil {
			return err
		}
		if b[0] == nackByte {
			return fmt.Errorf("芯片返回 NACK。复位后只发送同步字节 0x7F，帧格式保持 8N1")
		}
		s.log("已与 bootloader 同步")
		return nil
	}
	if last == nil {
		last = errTimeout
	}
	return fmt.Errorf("在 %s 内没有进入 bootloader：%w", window, last)
}

// Get reads the bootloader version and the command set.
func (s *Session) Get() (Info, error) {
	if err := s.command(cmdGet, time.Second); err != nil {
		return Info{}, fmt.Errorf("GET：%w", err)
	}
	body, err := s.readEcho(8, time.Second)
	if err != nil {
		return Info{}, fmt.Errorf("读取 GET 应答：%w", err)
	}
	if body[7] != ackByte {
		return Info{}, fmt.Errorf("GET 结束应答是 0x%02X", body[7])
	}
	info := Info{Version: body[1], Commands: append([]byte(nil), body[2:7]...)}
	s.log(fmt.Sprintf("bootloader 版本 %s", info.String()))
	return info, nil
}

// Read reads length bytes starting at addr. length is 1..65536 in practice;
// each bootloader transaction carries at most 128 bytes.
func (s *Session) Read(addr uint32, length int) ([]byte, error) {
	if length <= 0 {
		return nil, fmt.Errorf("读取长度必须大于 0")
	}
	out := make([]byte, 0, length)
	for length > 0 {
		n := length
		if n > readChunk {
			n = readChunk
		}
		chunk, err := s.readOnce(addr, n)
		if err != nil {
			return nil, fmt.Errorf("读取 0x%04X：%w", addr, err)
		}
		out = append(out, chunk...)
		addr += uint32(n)
		length -= n
	}
	return out, nil
}

func (s *Session) readOnce(addr uint32, n int) ([]byte, error) {
	if n < 1 || n > 256 {
		return nil, fmt.Errorf("单次读取 %d 字节超出 1–256", n)
	}
	if err := s.command(cmdRead, time.Second); err != nil {
		return nil, err
	}
	if err := s.sendAddr(addr, time.Second); err != nil {
		return nil, err
	}
	count := byte(n - 1)
	if err := writeFull(s.port, []byte{count, count ^ 0xFF}); err != nil {
		return nil, err
	}
	resp, err := s.readEcho(1+n, 2*time.Second)
	if err != nil {
		return nil, err
	}
	if resp[0] == nackByte {
		return nil, errNack
	}
	if resp[0] != ackByte {
		return nil, fmt.Errorf("读数据应答是 0x%02X", resp[0])
	}
	return resp[1:], nil
}

// Probe reports whether a one-byte read at addr is accepted.
func (s *Session) Probe(addr uint32) (bool, error) {
	_, err := s.readOnce(addr, 1)
	if errors.Is(err, errNack) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Write programs data at addr. The caller uploads the RAM routine first when
// the destination is flash, EEPROM or option bytes.
func (s *Session) Write(addr uint32, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	pages := image.Pages([]image.Run{{Addr: addr, Data: data}})
	for _, page := range pages {
		if err := s.writeOnce(page.Addr, page.Data); err != nil {
			return fmt.Errorf("写入 0x%04X（%d 字节）：%w", page.Addr, len(page.Data), err)
		}
	}
	return nil
}

func (s *Session) writeOnce(addr uint32, data []byte) error {
	if len(data) < 1 || len(data) > 128 {
		return fmt.Errorf("单次写入 %d 字节超出 1–128", len(data))
	}
	if err := s.command(cmdWrite, time.Second); err != nil {
		return err
	}
	if err := s.sendAddr(addr, time.Second); err != nil {
		return err
	}
	frame := make([]byte, 0, len(data)+2)
	count := byte(len(data) - 1)
	frame = append(frame, count)
	chk := count
	for _, b := range data {
		frame = append(frame, b)
		chk ^= b
	}
	frame = append(frame, chk)
	if err := writeFull(s.port, frame); err != nil {
		return err
	}
	return s.expectACK(5 * time.Second)
}

// WriteImage writes every stored byte, splitting on gaps and 128-byte pages.
func (s *Session) WriteImage(img *image.Image) error {
	for _, run := range img.Runs() {
		if err := s.Write(run.Addr, run.Data); err != nil {
			return err
		}
	}
	return nil
}

// EraseSectors erases the given UM0560 sector codes. N in the frame is the
// count minus one.
func (s *Session) EraseSectors(sectors []byte) error {
	if len(sectors) == 0 {
		return nil
	}
	if len(sectors) > 33 {
		return fmt.Errorf("一次最多擦除 33 个扇区，收到 %d 个", len(sectors))
	}
	if err := s.command(cmdErase, time.Second); err != nil {
		return fmt.Errorf("擦除命令：%w", err)
	}
	frame := make([]byte, 0, len(sectors)+2)
	count := byte(len(sectors) - 1)
	frame = append(frame, count)
	chk := count
	for _, sec := range sectors {
		frame = append(frame, sec)
		chk ^= sec
	}
	frame = append(frame, chk)
	if err := writeFull(s.port, frame); err != nil {
		return err
	}
	if err := s.expectACK(20 * time.Second); err != nil {
		return fmt.Errorf("擦除完成应答：%w", err)
	}
	return nil
}

// Go starts execution at addr. The bootloader then leaves command mode.
func (s *Session) Go(addr uint32) error {
	if err := s.command(cmdGo, time.Second); err != nil {
		return fmt.Errorf("GO：%w", err)
	}
	if err := s.sendAddr(addr, time.Second); err != nil {
		return fmt.Errorf("GO 地址：%w", err)
	}
	s.log(fmt.Sprintf("已从 0x%04X 启动", addr))
	return nil
}

func (s *Session) command(cmd byte, timeout time.Duration) error {
	if err := writeFull(s.port, []byte{cmd, cmd ^ 0xFF}); err != nil {
		return err
	}
	return s.expectACK(timeout)
}

func (s *Session) sendAddr(addr uint32, timeout time.Duration) error {
	b := []byte{byte(addr >> 24), byte(addr >> 16), byte(addr >> 8), byte(addr)}
	chk := b[0] ^ b[1] ^ b[2] ^ b[3]
	if err := writeFull(s.port, append(b, chk)); err != nil {
		return err
	}
	return s.expectACK(timeout)
}

func (s *Session) expectACK(timeout time.Duration) error {
	b, err := s.readEcho(1, timeout)
	if err != nil {
		return err
	}
	switch b[0] {
	case ackByte:
		return nil
	case nackByte:
		return errNack
	default:
		return fmt.Errorf("应答 0x%02X", b[0])
	}
}

// readEcho reads n bytes and echoes each one, which is what UART2 reply mode
// requires on a two-wire USB-TTL cable.
func (s *Session) readEcho(n int, timeout time.Duration) ([]byte, error) {
	out := make([]byte, 0, n)
	deadline := time.Now().Add(timeout)
	for len(out) < n {
		left := time.Until(deadline)
		if left <= 0 {
			return out, errTimeout
		}
		b, err := s.readRaw(1, left)
		if err != nil {
			return out, err
		}
		out = append(out, b[0])
		if err := writeFull(s.port, []byte{b[0]}); err != nil {
			return out, err
		}
	}
	return out, nil
}

func (s *Session) readRaw(n int, timeout time.Duration) ([]byte, error) {
	out := make([]byte, 0, n)
	deadline := time.Now().Add(timeout)
	tmp := make([]byte, 1)
	for len(out) < n {
		if time.Now().After(deadline) {
			return out, errTimeout
		}
		if err := s.port.SetReadDeadline(deadline); err != nil {
			return out, err
		}
		k, err := s.port.Read(tmp)
		if k == 1 {
			out = append(out, tmp[0])
			continue
		}
		if err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
			return out, errTimeout
		}
		return out, err
	}
	return out, nil
}

func writeFull(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if n > 0 {
			b = b[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

var (
	errTimeout = errors.New("超时")
	errNack    = errors.New("芯片返回 NACK")
)
