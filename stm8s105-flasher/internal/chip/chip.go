// Package chip describes the STM8S105 parts this flasher can program.
package chip

import "fmt"

// Model is one STM8S105 memory map. K4 and K6 share the UART2 reply-mode
// bootloader; only the flash size differs.
type Model struct {
	ID          string
	Title       string
	FlashStart  uint32
	FlashSize   uint32
	EEPROMStart uint32
	EEPROMSize  uint32
	OptionAddr  uint32
}

// K4 is STM8S105K4T6: LQFP32, 16 KB flash.
var K4 = Model{
	ID:          "STM8S105K4",
	Title:       "STM8S105K4T6（16 KB Flash）",
	FlashStart:  0x8000,
	FlashSize:   16 * 1024,
	EEPROMStart: 0x4000,
	EEPROMSize:  1024,
	OptionAddr:  0x4800,
}

// K6 is the 32 KB sibling. Same bootloader and the same UART2 pins.
var K6 = Model{
	ID:          "STM8S105K6",
	Title:       "STM8S105K6（32 KB Flash）",
	FlashStart:  0x8000,
	FlashSize:   32 * 1024,
	EEPROMStart: 0x4000,
	EEPROMSize:  1024,
	OptionAddr:  0x4800,
}

const (
	// OptionBlock is the 128-byte option-byte window programmed as one block.
	OptionBlock = 128
	// BootOPT is the bootloader enable byte inside that window (address 0x487E).
	BootOPT = 0x7E
	// BootNOPT is its complement (address 0x487F).
	BootNOPT = 0x7F
	// EEPROMSector is the UM0560 sector code for data EEPROM on medium-density STM8S.
	EEPROMSector = 0x20
	// SectorSize is the bootloader erase granularity.
	SectorSize = 0x400
)

// ByID returns a supported model. The empty id selects K4.
func ByID(id string) (Model, error) {
	switch id {
	case "", "k4", "K4", "STM8S105K4", "STM8S105K4T6":
		return K4, nil
	case "k6", "K6", "STM8S105K6", "STM8S105K6T6":
		return K6, nil
	default:
		return Model{}, fmt.Errorf("不认识的型号 %q，可选 k4 或 k6", id)
	}
}

// FlashEnd is the last program-memory address.
func (m Model) FlashEnd() uint32 { return m.FlashStart + m.FlashSize - 1 }

// ContainsFlash reports whether addr lies in program flash.
func (m Model) ContainsFlash(addr uint32) bool {
	return addr >= m.FlashStart && addr < m.FlashStart+m.FlashSize
}

// ContainsEEPROM reports whether addr lies in the data EEPROM.
func (m Model) ContainsEEPROM(addr uint32) bool {
	return addr >= m.EEPROMStart && addr < m.EEPROMStart+m.EEPROMSize
}

// ContainsOption reports whether addr lies in the 128-byte option block.
func (m Model) ContainsOption(addr uint32) bool {
	return addr >= m.OptionAddr && addr < m.OptionAddr+OptionBlock
}

// FlashSector returns the UM0560 sector code covering addr.
func (m Model) FlashSector(addr uint32) (byte, bool) {
	if !m.ContainsFlash(addr) {
		return 0, false
	}
	return byte((addr - m.FlashStart) / SectorSize), true
}

// FlashSectors lists every program-flash sector on this part.
func (m Model) FlashSectors() []byte {
	n := int(m.FlashSize / SectorSize)
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = byte(i)
	}
	return out
}
