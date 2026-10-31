package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"stm8s105-flasher/internal/boot"
	"stm8s105-flasher/internal/gui"
	"stm8s105-flasher/internal/image"
	"stm8s105-flasher/internal/port"
)

func main() {
	if len(os.Args) < 2 {
		if err := gui.Serve(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	switch os.Args[1] {
	case "gui":
		exitIf(gui.Serve())
	case "ports":
		names, err := port.List()
		exitIf(err)
		if len(names) == 0 {
			fmt.Println("没有发现串口")
			return
		}
		for _, name := range names {
			fmt.Println(name)
		}
	case "info":
		fs := flag.NewFlagSet("info", flag.ExitOnError)
		opt := commonFlags(fs)
		parse(fs)
		withSession(opt, func(s *boot.Session) error {
			_, model, err := boot.Prepare(s)
			if err != nil {
				return err
			}
			fmt.Println(model.Title)
			return nil
		})
	case "erase":
		fs := flag.NewFlagSet("erase", flag.ExitOnError)
		opt := commonFlags(fs)
		eeprom := fs.Bool("eeprom", false, "同时擦除数据 EEPROM")
		parse(fs)
		withSession(opt, func(s *boot.Session) error {
			_, model, err := boot.Prepare(s)
			if err != nil {
				return err
			}
			return boot.EraseFlash(s, *model, *eeprom)
		})
	case "write":
		fs := flag.NewFlagSet("write", flag.ExitOnError)
		opt := commonFlags(fs)
		file := fs.String("file", "", "固件路径，支持 hex、s19、bin")
		base := fs.Uint("base", 0x8000, "BIN 文件的起始地址")
		keep := fs.Bool("keep-bootloader", true, "烧录后把选项字节设为 0x55AA")
		verify := fs.Bool("verify", true, "烧录后读回校验")
		run := fs.Bool("go", false, "完成后从 0x8000 运行")
		eeprom := fs.Bool("eeprom", false, "同时擦除数据 EEPROM")
		parse(fs)
		if *file == "" {
			exitIf(fmt.Errorf("需要 -file"))
		}
		img, err := image.LoadFile(*file, uint32(*base))
		exitIf(err)
		withSession(opt, func(s *boot.Session) error {
			_, model, err := boot.Prepare(s)
			if err != nil {
				return err
			}
			if err := boot.Program(s, *model, img, *keep, *verify, *eeprom, nil); err != nil {
				return err
			}
			if *run {
				return s.Go(model.FlashStart)
			}
			return nil
		})
	case "read":
		fs := flag.NewFlagSet("read", flag.ExitOnError)
		opt := commonFlags(fs)
		out := fs.String("out", "stm8s105.hex", "读回内容保存为 Intel HEX")
		parse(fs)
		withSession(opt, func(s *boot.Session) error {
			_, model, err := boot.Prepare(s)
			if err != nil {
				return err
			}
			img, err := boot.ReadFlash(s, *model, nil)
			if err != nil {
				return err
			}
			return os.WriteFile(*out, img.IntelHex(), 0o644)
		})
	case "help", "-h", "-help", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}

type options struct {
	port  *string
	baud  *int
	reset *string
	wait  *int
}

func commonFlags(fs *flag.FlagSet) options {
	return options{
		port:  fs.String("port", "", "串口，例如 COM3 或 /dev/cu.usbserial-110"),
		baud:  fs.Int("baud", 115200, "波特率"),
		reset: fs.String("reset", "manual", "复位方式：manual、dtr、rts"),
		wait:  fs.Int("wait", 10, "等待复位进入 bootloader 的秒数"),
	}
}

func parse(fs *flag.FlagSet) {
	fs.Parse(os.Args[2:])
}

func withSession(opt options, fn func(*boot.Session) error) {
	if *opt.port == "" {
		exitIf(fmt.Errorf("需要 -port"))
	}
	window := time.Duration(*opt.wait) * time.Second
	fmt.Printf("打开 %s，%d 8N1\n", *opt.port, *opt.baud)
	s, p, err := boot.Connect(*opt.port, *opt.baud, *opt.reset, window, func(line string) {
		fmt.Println(line)
	})
	exitIf(err)
	defer p.Close()
	exitIf(fn(s))
}

func exitIf(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Println(`STM8S105K4 / K6 串口烧录

直接运行打开图形界面。命令行：

  stm8s105flash ports
  stm8s105flash info  -port COM3
  stm8s105flash write -port COM3 -file app.hex
  stm8s105flash erase -port COM3
  stm8s105flash read  -port COM3 -out dump.hex

接线：USB 转 TTL 的 TX 接芯片 PD6（LQFP32 的 31 脚），RX 接 PD5（30 脚），共地。
帧格式 8N1。空片上电会停在 bootloader；已经写过程序时，选项字节 0x487E/0x487F
必须是 0x55/0xAA，复位后大约 1 秒内要收到同步字节。`)
}
