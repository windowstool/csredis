//go:build darwin

package port

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type sysPort struct {
	f    *os.File
	baud int
}

func list() ([]string, error) {
	m, _ := filepath.Glob("/dev/cu.*")
	var names []string
	for _, name := range m {
		base := filepath.Base(name)
		if strings.Contains(base, "Bluetooth") || strings.Contains(base, "debug-console") || strings.Contains(base, "wlan") {
			continue
		}
		names = append(names, name)
	}
	SortNames(names)
	return names, nil
}

func open(name string, baud int) (Port, error) {
	f, err := os.OpenFile(name, os.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("打开 %s：%w", name, err)
	}
	fd := int(f.Fd())
	speed, err := baudBits(baud)
	if err != nil {
		f.Close()
		return nil, err
	}
	t := unix.Termios{
		Cflag:  unix.CREAD | unix.CLOCAL | unix.CS8 | speed,
		Ispeed: speed,
		Ospeed: speed,
	}
	t.Cc[unix.VMIN] = 0
	t.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, &t); err != nil {
		f.Close()
		return nil, fmt.Errorf("设置 %s 的串口参数：%w", name, err)
	}
	if err := unix.SetNonblock(fd, false); err != nil {
		f.Close()
		return nil, err
	}
	p := &sysPort{f: f, baud: baud}
	_ = p.SetDTR(true)
	_ = p.SetRTS(true)
	_ = p.Flush()
	return p, nil
}

func baudBits(baud int) (uint64, error) {
	switch baud {
	case 4800:
		return unix.B4800, nil
	case 9600:
		return unix.B9600, nil
	case 19200:
		return unix.B19200, nil
	case 38400:
		return unix.B38400, nil
	case 57600:
		return unix.B57600, nil
	case 115200:
		return unix.B115200, nil
	case 230400:
		return unix.B230400, nil
	default:
		return 0, fmt.Errorf("macOS 版目前支持 4800、9600、19200、38400、57600、115200、230400，收到 %d", baud)
	}
}

func (p *sysPort) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *sysPort) Write(b []byte) (int, error) { return p.f.Write(b) }
func (p *sysPort) Close() error                { return p.f.Close() }

func (p *sysPort) SetReadDeadline(t timeDeadline) error { return p.f.SetReadDeadline(t) }

func (p *sysPort) SetDTR(level bool) error { return p.setLine(unix.TIOCM_DTR, level) }
func (p *sysPort) SetRTS(level bool) error { return p.setLine(unix.TIOCM_RTS, level) }

func (p *sysPort) setLine(bit int, level bool) error {
	status, err := unix.IoctlGetInt(int(p.f.Fd()), unix.TIOCMGET)
	if err != nil {
		return err
	}
	if level {
		status |= bit
	} else {
		status &^= bit
	}
	return unix.IoctlSetPointerInt(int(p.f.Fd()), unix.TIOCMSET, status)
}

func (p *sysPort) Flush() error {
	// FREAD|FWRITE, the Darwin TIOCFLUSH selector.
	const discard = 1 | 2
	return unix.IoctlSetPointerInt(int(p.f.Fd()), unix.TIOCFLUSH, discard)
}
