# switchbt-procon

独立的 Go 守护进程，用蓝牙把本机模拟成 Nintendo Switch `Pro Controller`，并通过 **Unix Domain Socket + HTTP JSON** 接收外部进程输入。

## 设计目标

1. **不改 `kvmserver` 现有代码**
2. 作为单独进程运行在设备上
3. 后续由 `kvmserver` 把前端手柄数据转发到本进程
4. 首次连接走 **Switch -> Change Grip/Order**
5. 成功配对后可记录 `PairedSwitch` 地址，后续走重连模式

## 依赖

- Go 1.24+
- BlueZ / `bluetoothd`
- 设备具备可用蓝牙适配器
- `hciconfig` 命令可用（用于设置 gamepad class）

## 运行

```bash
cd /home/blikvm/Downloads/switchbt-procon
go run .
```

自动进入配对模式：

```bash
go run . --auto-pair --adapter hci0
```

自动重连到已知 Switch：

```bash
go run . --adapter hci0 --reconnect AA:BB:CC:DD:EE:FF
```

默认 IPC socket:

```text
/tmp/switchbt-procon.sock
```

## IPC 设计

本进程暴露 **HTTP over Unix socket**：

- `GET /status`
- `POST /start`
- `POST /stop`
- `POST /input`

这种方式适合后续在 Go 里的 `kvmserver` 直接通过 Unix socket 发请求，无需再造一层私有协议。

### 1. 查询状态

```bash
curl --unix-socket /tmp/switchbt-procon.sock http://localhost/status
```

### 2. 首次配对启动

```bash
curl --unix-socket /tmp/switchbt-procon.sock \
  -H 'Content-Type: application/json' \
  -d '{"adapter":"hci0"}' \
  http://localhost/start
```

然后在 Switch 打开 **Change Grip/Order**。

> 如果你使用 `--auto-pair` 启动参数，这一步会自动开始，不需要再调 `/start`。

### 3. 重连模式

```bash
curl --unix-socket /tmp/switchbt-procon.sock \
  -H 'Content-Type: application/json' \
  -d '{"adapter":"hci0","reconnectAddr":"AA:BB:CC:DD:EE:FF"}' \
  http://localhost/start
```

### 4. 发送输入

支持两种 JSON：

```json
{
  "gp": {
    "dpad": {"up":0,"down":0,"left":0,"right":0},
    "button": {"a":0,"b":0,"x":0,"y":0,"l":0,"r":0,"zl":0,"zr":0,"minus":0,"plus":0,"home":0,"capture":0},
    "stick": {
      "left": {"x":0,"y":0,"press":0},
      "right": {"x":0,"y":0,"press":0}
    }
  }
}
```

或者直接传 `SwitchProConInput` 本体。

```bash
curl --unix-socket /tmp/switchbt-procon.sock \
  -H 'Content-Type: application/json' \
  -d '{"gp":{"button":{"a":1},"stick":{"left":{"x":0,"y":0},"right":{"x":0,"y":0}}}}' \
  http://localhost/input
```

## 当前实现范围

- BlueZ adapter 配置
- HID SDP 注册
- L2CAP PSM 17 / 19
- `joycontrol` 里最关键的若干 `subcommand` 应答：
  - `0x02` device info
  - `0x03` set input report mode
  - `0x04` trigger buttons elapsed time
  - `0x08` shipment state
  - `0x10` SPI flash read
  - `0x30` set player lights
  - `0x40` enable 6axis
  - `0x48` enable vibration
- 0x30 持续状态上报

## 预期与限制

- 这是基于 `joycontrol` 思路做的 **Go 最小版本**
- 目前优先支持 `PRO_CONTROLLER`
- 重点是打通：
  - Switch 能配对
  - 后端能喂按钮/摇杆
  - Switch 能收到蓝牙手柄输入

后续如果需要，再继续补：

- 更完整的 output report 处理
- 重连状态持久化
- 更细致的 BlueZ 错误恢复
- 振动/IMU/NFC


 cd /home/blikvm/Downloads/switchbt-procon
 
 go mod tidy
 
 CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
   go build -trimpath -ldflags="-s -w" -o switchbt-procon




 /usr/bin/hciattach /dev/ttyS1 any 1500000 flow &
 sleep 1
 hciconfig hci0 up
 /usr/bin/dbus-daemon --system --fork --nopidfile
 /usr/libexec/bluetooth/bluetoothd -n -d -P input &
 /userdata/server/bin/switchbt-procon --auto-pair --adapter hci0
