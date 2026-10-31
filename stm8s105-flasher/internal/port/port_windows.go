//go:build windows

package port

import (
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

type sysPort struct {
	h    windows.Handle
	baud int
}

func list() ([]string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DEVICEMAP\SERIALCOMM`, registry.QUERY_VALUE)
	if err != nil {
		return nil, nil
	}
	defer k.Close()
	names, err := k.ReadValueNames(-1)
	if err != nil {
		return nil, err
	}
	var ports []string
	for _, name := range names {
		v, _, err := k.GetStringValue(name)
		if err == nil && v != "" {
			ports = append(ports, v)
		}
	}
	SortNames(ports)
	return ports, nil
}

func open(name string, baud int) (Port, error) {
	path := name
	if len(name) > 0 && name[0] != '\\' {
		path = `\\.\` + name
	}
	h, err := windows.CreateFile(windows.StringToUTF16Ptr(path), windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("打开 %s：%w", name, err)
	}
	p := &sysPort{h: h, baud: baud}
	if err := p.configure(); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	_ = p.SetDTR(true)
	_ = p.SetRTS(true)
	_ = p.Flush()
	return p, nil
}

func (p *sysPort) configure() error {
	var dcb windows.DCB
	dcb.DCBlength = uint32(unsafe.Sizeof(dcb))
	if err := windows.GetCommState(p.h, &dcb); err != nil {
		return err
	}
	dcb.BaudRate = uint32(p.baud)
	dcb.ByteSize = 8
	dcb.Parity = windows.NOPARITY
	dcb.StopBits = windows.ONESTOPBIT
	// fBinary is bit 0. fParity is bit 1 and stays clear for 8N1.
	dcb.Flags |= 1
	dcb.Flags &^= 2
	if err := windows.SetCommState(p.h, &dcb); err != nil {
		return fmt.Errorf("设置串口参数：%w", err)
	}
	return p.setTimeouts(200 * time.Millisecond)
}

func (p *sysPort) setTimeouts(d time.Duration) error {
	ms := uint32(d / time.Millisecond)
	if ms == 0 {
		ms = 1
	}
	return windows.SetCommTimeouts(p.h, &windows.CommTimeouts{
		ReadIntervalTimeout:        20,
		ReadTotalTimeoutMultiplier: 0,
		ReadTotalTimeoutConstant:   ms,
		WriteTotalTimeoutConstant:  2000,
	})
}

func (p *sysPort) Read(b []byte) (int, error) {
	var n uint32
	err := windows.ReadFile(p.h, b, &n, nil)
	if n == 0 && err == nil {
		return 0, osDeadline
	}
	if err == windows.ERROR_TIMEOUT || err == windows.WAIT_TIMEOUT {
		return int(n), osDeadline
	}
	return int(n), err
}

func (p *sysPort) Write(b []byte) (int, error) {
	var n uint32
	err := windows.WriteFile(p.h, b, &n, nil)
	return int(n), err
}

func (p *sysPort) Close() error { return windows.CloseHandle(p.h) }

func (p *sysPort) SetReadDeadline(t time.Time) error {
	d := time.Until(t)
	if d <= 0 {
		d = time.Millisecond
	}
	return p.setTimeouts(d)
}

func (p *sysPort) SetDTR(level bool) error { return escape(p.h, level, windows.SETDTR, windows.CLRDTR) }
func (p *sysPort) SetRTS(level bool) error { return escape(p.h, level, windows.SETRTS, windows.CLRRTS) }

func escape(h windows.Handle, level bool, on, off uint32) error {
	fn := off
	if level {
		fn = on
	}
	return windows.EscapeCommFunction(h, fn)
}

func (p *sysPort) Flush() error {
	return windows.PurgeComm(p.h, windows.PURGE_RXCLEAR|windows.PURGE_TXCLEAR)
}
