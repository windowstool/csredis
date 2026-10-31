# STM8S105 串口烧录

给 **STM8S105K4T6**（以及同系列 32 KB 的 STM8S105K6）用的串口烧录程序。同一份代码可以在 Windows 和 macOS 上编译，打开图形界面，也可以在命令行里烧录。

芯片走的是 ROM bootloader 的 **UART2 reply 模式**（ST UM0560）：8 数据位、无校验、1 停止位。主机要把芯片发出的每个字节原样回显。

## 接线

LQFP32：

| 芯片 | 引脚 | 接到 USB 转 TTL |
| --- | --- | --- |
| PD5 / UART2_TX | 30 | RX |
| PD6 / UART2_RX | 31 | TX |
| GND | | GND |

SWIM 在 PD1（26 脚），给 ST-Link 用。串口烧录不经过 SWIM。

空片上电会停在 bootloader。程序写进 Flash 之后，选项字节 `0x487E = 0x55`、`0x487F = 0xAA` 时，复位后大约 1 秒内收到 `0x7F` 才会再次进入。图形界面默认会在烧录结束时写上这对字节。

## 图形界面

不带参数运行：

```text
stm8s105flash
```

程序会在本机打开浏览器页面。选择串口和固件后点烧录。复位方式选「手动」时，开始后按一下板子上的复位键。

固件可以是 Intel HEX（`.hex` / `.ihx`）、Motorola S-record（`.s19`）或原始二进制（`.bin`，从 `0x8000` 放起）。

## 命令行

```text
stm8s105flash ports
stm8s105flash info  -port COM3
stm8s105flash write -port COM3 -file app.hex
stm8s105flash erase -port COM3
stm8s105flash read  -port COM3 -out dump.hex
```

macOS 上的串口名类似 `/dev/cu.usbserial-110`。常用参数：

- `-baud 115200`
- `-reset manual`、`-reset dtr` 或 `-reset rts`
- `-wait 10`：等待进入 bootloader 的秒数
- `-keep-bootloader=false`：不改选项字节
- `-verify=false`：跳过读回校验
- `-go`：烧完从 `0x8000` 运行
- `-eeprom`：连数据 EEPROM 一起擦除

## 编译

需要 Go 1.22 或更新版本。在 `stm8s105-flasher` 目录：

```text
sh scripts/build.sh
```

会生成：

- `dist/stm8s105flash-windows-amd64.exe`
- `dist/stm8s105flash-windows-arm64.exe`
- `dist/stm8s105flash-macos-amd64`
- `dist/stm8s105flash-macos-arm64`

macOS 上如果系统拦截未签名程序，在终端里运行该文件，或在「隐私与安全性」里允许它。

## 烧录时芯片里发生的事

1. 主机发送 `0x7F`，芯片识别波特率并回答 `0x79`。
2. 读取 bootloader 版本。中密度 STM8S105 常见是 1.3 或 1.4。
3. 把对应的擦写程序写到 RAM（擦除入口 `0x00A0`，写入入口 `0x0150`）。ROM 自己不能擦写 Flash，必须先放这段程序。
4. 按 1 KB 扇区擦除，再按最多 128 字节一页写入。
5. 需要的话改写选项字节，然后按固件地址读回比较。

K4 的 Flash 是 `0x8000`–`0xBFFF`（16 KB）。程序会先读 `0xFFFF`：读成功就是 32 KB 的 K6，读失败再确认 `0xBFFF`，从而区分这两颗料。全片擦除只擦当前容量里的扇区，避免 K4 去擦并不存在的高 16 KB。

## RAM 擦写程序

`ramcode/routines` 里的 Intel HEX 来自 Basil Hussain 的开源实现，Apache License 2.0，说明见 `ramcode/NOTICE` 和 `ramcode/LICENSE.txt`。
