// Package ramcode holds the RAM-resident erase/write routines the STM8S105
// ROM bootloader calls before it can modify flash or EEPROM.
package ramcode

import (
	"embed"
	"fmt"

	"stm8s105-flasher/internal/image"
)

//go:embed routines/*.ihx
var files embed.FS

// versions maps a BCD bootloader version byte (0x13 == v1.3) to its image.
var versions = map[byte]string{
	0x10: "routines/erase_write_ver_32k_1.0.ihx",
	0x12: "routines/erase_write_ver_32k_1.2.ihx",
	0x13: "routines/erase_write_ver_32k_1.3.ihx",
	0x14: "routines/erase_write_ver_32k_1.4.ihx",
}

// ForVersion returns the routine image for a medium-density STM8S bootloader.
// The image covers the erase entry at 0x00A0, the write entry at 0x0150 and
// the shared code at 0x0300.
func ForVersion(version byte) (*image.Image, error) {
	name, ok := versions[version]
	if !ok {
		return nil, fmt.Errorf("没有适用于 bootloader 版本 %d.%d 的 RAM 擦写程序（已支持 1.0、1.2、1.3、1.4）", version>>4, version&0x0F)
	}
	raw, err := files.ReadFile(name)
	if err != nil {
		return nil, err
	}
	img, err := image.Parse(name, raw, 0)
	if err != nil {
		return nil, fmt.Errorf("解析 RAM 擦写程序：%w", err)
	}
	for _, addr := range []uint32{0x00A0, 0x0150, 0x0300} {
		if _, ok := img.Data[addr]; !ok {
			return nil, fmt.Errorf("RAM 擦写程序缺少入口 0x%04X", addr)
		}
	}
	return img, nil
}
