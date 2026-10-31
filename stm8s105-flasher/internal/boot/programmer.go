package boot

import (
	"bytes"
	"fmt"
	"time"

	"stm8s105-flasher/internal/chip"
	"stm8s105-flasher/internal/image"
	"stm8s105-flasher/internal/port"
	"stm8s105-flasher/ramcode"
)

// Progress reports a fraction from 0 to 1.
type Progress func(float64)

// Connect opens the port, optionally pulses a modem line, and synchronises.
func Connect(name string, baud int, reset string, window time.Duration, log func(string)) (*Session, port.Port, error) {
	p, err := port.Open(name, baud)
	if err != nil {
		return nil, nil, err
	}
	s := NewSession(p, log)
	if err := pulseReset(p, reset, log); err != nil {
		p.Close()
		return nil, nil, err
	}
	if reset == "" || reset == "none" || reset == "manual" {
		log("请在倒计时内按一下开发板复位键（NRST）")
	}
	if err := s.Sync(window); err != nil {
		p.Close()
		return nil, nil, err
	}
	return s, p, nil
}

func pulseReset(p port.Port, reset string, log func(string)) error {
	switch reset {
	case "", "none", "manual":
		return nil
	case "dtr":
		log("通过 DTR 脉冲复位")
		return pulse(p.SetDTR)
	case "rts":
		log("通过 RTS 脉冲复位")
		return pulse(p.SetRTS)
	default:
		return fmt.Errorf("复位方式 %q 应为 manual、dtr 或 rts", reset)
	}
}

func pulse(set func(bool) error) error {
	if err := set(false); err != nil {
		return err
	}
	time.Sleep(100 * time.Millisecond)
	if err := set(true); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	return nil
}

// Prepare synchronises an already connected session and loads the RAM routine
// that matches the ROM version.
func Prepare(s *Session) (Info, *chip.Model, error) {
	info, err := s.Get()
	if err != nil {
		return Info{}, nil, err
	}
	routine, err := ramcode.ForVersion(info.Version)
	if err != nil {
		return info, nil, err
	}
	s.log(fmt.Sprintf("写入 RAM 擦写程序，%d 字节", routine.Len()))
	if err := s.WriteImage(routine); err != nil {
		return info, nil, fmt.Errorf("写入 RAM 擦写程序：%w", err)
	}
	model, err := detectModel(s)
	if err != nil {
		return info, nil, err
	}
	s.log("识别为 " + model.Title)
	return info, model, nil
}

func detectModel(s *Session) (*chip.Model, error) {
	// 0xFFFF is inside a 32 KB part and outside a 16 KB part.
	has32, err := s.Probe(0x00FFFF)
	if err != nil {
		return nil, fmt.Errorf("探测 Flash 容量：%w", err)
	}
	if has32 {
		m := chip.K6
		return &m, nil
	}
	has16, err := s.Probe(0x00BFFF)
	if err != nil {
		return nil, fmt.Errorf("探测 Flash 容量：%w", err)
	}
	if has16 {
		m := chip.K4
		return &m, nil
	}
	return nil, fmt.Errorf("读不到 STM8S105 的 Flash。请确认芯片是 STM8S105K4 或 K6，并且 bootloader 已启动")
}

// EraseFlash erases every program-flash sector. EEPROM is included only when asked.
func EraseFlash(s *Session, m chip.Model, eeprom bool) error {
	sectors := m.FlashSectors()
	if eeprom {
		sectors = append(sectors, chip.EEPROMSector)
		s.log("擦除全部 Flash 和数据 EEPROM")
	} else {
		s.log(fmt.Sprintf("擦除全部 Flash（%d KB）", m.FlashSize/1024))
	}
	return s.EraseSectors(sectors)
}

// Program erases the sectors covered by img, writes it, optionally keeps the
// UART bootloader enabled, and optionally reads the image back.
func Program(s *Session, m chip.Model, img *image.Image, keepBoot, verify, eeprom bool, progress Progress) error {
	if img == nil || img.Len() == 0 {
		return fmt.Errorf("固件是空的")
	}
	if err := validate(m, img); err != nil {
		return err
	}
	sectors := sectorsFor(m, img, eeprom)
	s.log(fmt.Sprintf("擦除 %d 个扇区", len(sectors)))
	if progress != nil {
		progress(0.05)
	}
	if err := s.EraseSectors(sectors); err != nil {
		return err
	}
	pages := image.Pages(img.Runs())
	s.log(fmt.Sprintf("写入 %d 字节，分 %d 页", img.Len(), len(pages)))
	for i, page := range pages {
		if err := s.Write(page.Addr, page.Data); err != nil {
			return err
		}
		if progress != nil {
			progress(0.1 + 0.6*float64(i+1)/float64(len(pages)))
		}
	}
	if keepBoot {
		if err := enableBootloader(s); err != nil {
			return err
		}
	}
	if verify {
		s.log("读回校验")
		if err := verifyImage(s, img, progress); err != nil {
			return err
		}
		s.log("校验通过")
	}
	if progress != nil {
		progress(1)
	}
	return nil
}

func validate(m chip.Model, img *image.Image) error {
	for addr := range img.Data {
		if m.ContainsFlash(addr) || m.ContainsEEPROM(addr) || m.ContainsOption(addr) {
			continue
		}
		return fmt.Errorf("地址 0x%04X 不在 %s 的 Flash、EEPROM 或选项字节范围内", addr, m.ID)
	}
	return nil
}

func sectorsFor(m chip.Model, img *image.Image, eeprom bool) []byte {
	seen := map[byte]bool{}
	var out []byte
	add := func(sec byte) {
		if seen[sec] {
			return
		}
		seen[sec] = true
		out = append(out, sec)
	}
	for addr := range img.Data {
		if sec, ok := m.FlashSector(addr); ok {
			add(sec)
		}
		if m.ContainsEEPROM(addr) {
			add(chip.EEPROMSector)
		}
	}
	if eeprom {
		add(chip.EEPROMSector)
	}
	// Stable order: flash sectors ascending, EEPROM last.
	sortSectors(out)
	return out
}

func sortSectors(s []byte) {
	for i := 1; i < len(s); i++ {
		j := i
		for j > 0 && s[j] < s[j-1] {
			s[j], s[j-1] = s[j-1], s[j]
			j--
		}
	}
}

func enableBootloader(s *Session) error {
	s.log("写入选项字节 0x487E=0x55、0x487F=0xAA，使下次复位仍能进入串口 bootloader")
	first, err := s.Read(0x4800, chip.OptionBlock)
	if err != nil {
		return fmt.Errorf("读取选项字节：%w", err)
	}
	second, err := s.Read(0x4800, chip.OptionBlock)
	if err != nil {
		return fmt.Errorf("再次读取选项字节：%w", err)
	}
	if !bytes.Equal(first, second) {
		return fmt.Errorf("两次读到的选项字节不一致，已停止写入，避免改坏读保护")
	}
	if first[0] == 0xAA {
		return fmt.Errorf("读保护字节是 0xAA，已停止改写选项字节")
	}
	if first[chip.BootOPT] == 0x55 && first[chip.BootNOPT] == 0xAA {
		s.log("bootloader 选项字节已经是 0x55AA")
		return nil
	}
	block := append([]byte(nil), first...)
	block[chip.BootOPT] = 0x55
	block[chip.BootNOPT] = 0xAA
	if err := s.Write(0x4800, block); err != nil {
		return fmt.Errorf("写入选项字节：%w。程序已经写入 Flash，但下次复位可能不再进入串口 bootloader，需要用 SWIM 重新打开选项字节", err)
	}
	got, err := s.Read(0x487E, 2)
	if err != nil {
		return fmt.Errorf("回读选项字节：%w", err)
	}
	if got[0] != 0x55 || got[1] != 0xAA {
		return fmt.Errorf("选项字节回读为 %02X %02X，期望 55 AA", got[0], got[1])
	}
	s.log("选项字节已更新")
	return nil
}

func verifyImage(s *Session, img *image.Image, progress Progress) error {
	runs := img.Runs()
	done := 0
	total := img.Len()
	for _, run := range runs {
		got, err := s.Read(run.Addr, len(run.Data))
		if err != nil {
			return fmt.Errorf("校验读取 0x%04X：%w", run.Addr, err)
		}
		if !bytes.Equal(got, run.Data) {
			for i := range got {
				if got[i] != run.Data[i] {
					return fmt.Errorf("校验失败：地址 0x%04X 期望 0x%02X，读回 0x%02X", run.Addr+uint32(i), run.Data[i], got[i])
				}
			}
		}
		done += len(run.Data)
		if progress != nil && total > 0 {
			progress(0.75 + 0.25*float64(done)/float64(total))
		}
	}
	return nil
}

// ReadFlash reads the whole program flash into an image.
func ReadFlash(s *Session, m chip.Model, progress Progress) (*image.Image, error) {
	img := image.New()
	const step = 128
	total := int(m.FlashSize)
	for off := 0; off < total; off += step {
		n := step
		if off+n > total {
			n = total - off
		}
		addr := m.FlashStart + uint32(off)
		chunk, err := s.Read(addr, n)
		if err != nil {
			return nil, err
		}
		for i, b := range chunk {
			img.Put(addr+uint32(i), b)
		}
		if progress != nil {
			progress(float64(off+n) / float64(total))
		}
	}
	return img, nil
}
