// Package port lists and opens serial ports on Windows, macOS and Linux.
package port

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// Port is a byte pipe with modem-control lines and a read deadline.
type Port interface {
	io.ReadWriteCloser
	SetReadDeadline(time.Time) error
	SetDTR(level bool) error
	SetRTS(level bool) error
	Flush() error
}

// Open opens name at baud with 8 data bits, no parity and 1 stop bit.
func Open(name string, baud int) (Port, error) {
	if name == "" {
		return nil, fmt.Errorf("没有指定串口")
	}
	if baud < 4800 || baud > 1000000 {
		return nil, fmt.Errorf("波特率 %d 超出 bootloader 范围（4800–1000000）", baud)
	}
	return open(name, baud)
}

var comNum = regexp.MustCompile(`(?i)^COM(\d+)$`)

// SortNames orders COM10 after COM3 and keeps other names alphabetical.
func SortNames(names []string) {
	sort.Slice(names, func(i, j int) bool {
		ai, aok := comIndex(names[i])
		bi, bok := comIndex(names[j])
		if aok && bok {
			return ai < bi
		}
		if aok != bok {
			return aok
		}
		return names[i] < names[j]
	})
}

func comIndex(name string) (int, bool) {
	m := comNum.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}
