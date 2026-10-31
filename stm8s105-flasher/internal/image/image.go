// Package image reads and writes Intel HEX, Motorola S-record and raw binary files.
package image

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// Image is a sparse byte map keyed by absolute address.
type Image struct {
	Data map[uint32]byte
}

// New returns an empty image.
func New() *Image { return &Image{Data: map[uint32]byte{}} }

// Len is the number of stored bytes.
func (m *Image) Len() int { return len(m.Data) }

// Put stores one byte.
func (m *Image) Put(addr uint32, b byte) {
	if m.Data == nil {
		m.Data = map[uint32]byte{}
	}
	m.Data[addr] = b
}

// Range returns the lowest and highest stored addresses.
func (m *Image) Range() (uint32, uint32, bool) {
	if len(m.Data) == 0 {
		return 0, 0, false
	}
	var lo, hi uint32
	first := true
	for a := range m.Data {
		if first || a < lo {
			lo = a
		}
		if first || a > hi {
			hi = a
		}
		first = false
	}
	return lo, hi, true
}

// Run is a contiguous address range.
type Run struct {
	Addr uint32
	Data []byte
}

// Runs returns contiguous slices in ascending address order.
func (m *Image) Runs() []Run {
	if len(m.Data) == 0 {
		return nil
	}
	addrs := make([]uint32, 0, len(m.Data))
	for a := range m.Data {
		addrs = append(addrs, a)
	}
	sort.Slice(addrs, func(i, j int) bool { return addrs[i] < addrs[j] })
	var runs []Run
	start := addrs[0]
	buf := []byte{m.Data[start]}
	prev := start
	flush := func() {
		runs = append(runs, Run{Addr: start, Data: append([]byte(nil), buf...)})
	}
	for _, a := range addrs[1:] {
		if a == prev+1 {
			buf = append(buf, m.Data[a])
		} else {
			flush()
			start = a
			buf = []byte{m.Data[a]}
		}
		prev = a
	}
	flush()
	return runs
}

// Page is one bootloader write (at most 128 bytes), split on 128-byte boundaries
// so a full aligned page can use the chip's block program.
type Page struct {
	Addr uint32
	Data []byte
}

// Pages splits runs into bootloader writes of at most 128 bytes.
func Pages(runs []Run) []Page {
	var pages []Page
	for _, run := range runs {
		addr := run.Addr
		data := run.Data
		for len(data) > 0 {
			n := 1
			for n < 128 && n < len(data) && (addr+uint32(n))%128 != 0 {
				n++
			}
			pages = append(pages, Page{Addr: addr, Data: append([]byte(nil), data[:n]...)})
			addr += uint32(n)
			data = data[n:]
		}
	}
	return pages
}

// LoadFile parses a firmware file. Raw binaries are placed at binBase.
func LoadFile(path string, binBase uint32) (*Image, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(filepath.Base(path), b, binBase)
}

// Parse detects Intel HEX, S-record or raw binary from the name and contents.
func Parse(name string, content []byte, binBase uint32) (*Image, error) {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".hex", ".ihx", ".ihex":
		return parseIntelHex(content)
	case ".s19", ".s28", ".s37", ".srec", ".mot":
		return parseSRec(content)
	case ".bin", ".raw":
		return parseBin(content, binBase), nil
	}
	trim := bytes.TrimSpace(content)
	if len(trim) > 0 && trim[0] == ':' {
		return parseIntelHex(content)
	}
	if len(trim) > 2 && (trim[0] == 'S' || trim[0] == 's') && unicode.IsDigit(rune(trim[1])) {
		return parseSRec(content)
	}
	return parseBin(content, binBase), nil
}

func parseBin(content []byte, base uint32) *Image {
	m := New()
	for i, b := range content {
		m.Put(base+uint32(i), b)
	}
	return m
}

func parseIntelHex(content []byte) (*Image, error) {
	m := New()
	var upper uint32
	sc := bufio.NewScanner(bytes.NewReader(content))
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line[0] != ':' {
			return nil, fmt.Errorf("HEX 第 %d 行不是以 : 开头", lineNo)
		}
		raw, err := hex.DecodeString(line[1:])
		if err != nil {
			return nil, fmt.Errorf("HEX 第 %d 行：%w", lineNo, err)
		}
		if len(raw) < 5 {
			return nil, fmt.Errorf("HEX 第 %d 行太短", lineNo)
		}
		n := int(raw[0])
		if len(raw) != n+5 {
			return nil, fmt.Errorf("HEX 第 %d 行长度不符", lineNo)
		}
		var sum byte
		for _, b := range raw {
			sum += b
		}
		if sum != 0 {
			return nil, fmt.Errorf("HEX 第 %d 行校验和错误", lineNo)
		}
		addr := uint32(raw[1])<<8 | uint32(raw[2])
		kind := raw[3]
		data := raw[4 : 4+n]
		switch kind {
		case 0x00:
			for i, b := range data {
				m.Put(upper+addr+uint32(i), b)
			}
		case 0x01:
			// end of file
		case 0x02:
			if n != 2 {
				return nil, fmt.Errorf("HEX 第 %d 行扩展段地址长度错误", lineNo)
			}
			upper = (uint32(data[0])<<8 | uint32(data[1])) << 4
		case 0x04:
			if n != 2 {
				return nil, fmt.Errorf("HEX 第 %d 行扩展线性地址长度错误", lineNo)
			}
			upper = (uint32(data[0])<<8 | uint32(data[1])) << 16
		case 0x03, 0x05:
			// start address records carry no memory bytes
		default:
			return nil, fmt.Errorf("HEX 第 %d 行未知记录类型 0x%02X", lineNo, kind)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if m.Len() == 0 {
		return nil, fmt.Errorf("HEX 文件里没有数据")
	}
	return m, nil
}

func parseSRec(content []byte) (*Image, error) {
	m := New()
	sc := bufio.NewScanner(bytes.NewReader(content))
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if line[0] != 'S' && line[0] != 's' {
			return nil, fmt.Errorf("S-record 第 %d 行不是以 S 开头", lineNo)
		}
		if len(line) < 4 {
			return nil, fmt.Errorf("S-record 第 %d 行太短", lineNo)
		}
		kind := line[1]
		raw, err := hex.DecodeString(line[2:])
		if err != nil {
			return nil, fmt.Errorf("S-record 第 %d 行：%w", lineNo, err)
		}
		if len(raw) < 1 {
			return nil, fmt.Errorf("S-record 第 %d 行没有计数字节", lineNo)
		}
		count := int(raw[0])
		if len(raw) != count+1 {
			return nil, fmt.Errorf("S-record 第 %d 行长度不符", lineNo)
		}
		var sum byte
		for _, b := range raw {
			sum += b
		}
		if sum != 0xFF {
			return nil, fmt.Errorf("S-record 第 %d 行校验和错误", lineNo)
		}
		addrLen := 0
		switch kind {
		case '1':
			addrLen = 2
		case '2':
			addrLen = 3
		case '3':
			addrLen = 4
		case '0', '5', '6', '7', '8', '9':
			continue
		default:
			return nil, fmt.Errorf("S-record 第 %d 行未知类型 S%c", lineNo, kind)
		}
		if count < addrLen+1 {
			return nil, fmt.Errorf("S-record 第 %d 行地址不完整", lineNo)
		}
		var addr uint32
		for i := 0; i < addrLen; i++ {
			addr = addr<<8 | uint32(raw[1+i])
		}
		data := raw[1+addrLen : len(raw)-1]
		for i, b := range data {
			m.Put(addr+uint32(i), b)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if m.Len() == 0 {
		return nil, fmt.Errorf("S-record 文件里没有数据")
	}
	return m, nil
}

// IntelHex encodes the image as Intel HEX with 16-byte records.
func (m *Image) IntelHex() []byte {
	var b strings.Builder
	for _, run := range m.Runs() {
		for off := 0; off < len(run.Data); {
			n := 16
			if n > len(run.Data)-off {
				n = len(run.Data) - off
			}
			addr := run.Addr + uint32(off)
			// Stay inside a 64 KB linear window.
			if int(addr&0xFFFF)+n > 0x10000 {
				n = 0x10000 - int(addr&0xFFFF)
			}
			upper := addr >> 16
			fmt.Fprintf(&b, ":02000004%04X%02X\n", upper, hexSum([]byte{0x02, 0x00, 0x00, 0x04, byte(upper >> 8), byte(upper)}))
			rec := make([]byte, 0, 4+n)
			rec = append(rec, byte(n), byte(addr>>8), byte(addr), 0x00)
			rec = append(rec, run.Data[off:off+n]...)
			fmt.Fprintf(&b, ":%s%02X\n", strings.ToUpper(hex.EncodeToString(rec)), hexSum(rec))
			off += n
		}
	}
	b.WriteString(":00000001FF\n")
	return []byte(b.String())
}

func hexSum(rec []byte) byte {
	var s byte
	for _, x := range rec {
		s += x
	}
	return -s
}
