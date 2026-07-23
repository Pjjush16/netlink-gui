# NetLink GUI - 跨平台虚拟组网工具

基于 Fyne 框架的图形界面客户端，一次编译，到处运行。

## 特性

- 🖥️ **桌面支持**: Linux / Windows / macOS
- 📱 **移动支持**: Android / iOS
- 🌐 **无服务器 P2P**: 可选 InfinityFree 信令服务器
- 🔒 **端到端加密**: X25519 + ChaCha20-Poly1305
- 🚀 **双协议**: UDP（低延迟）+ TCP（穿透强）
- 🌍 **NAT 穿透**: STUN 自动发现公网地址
- 🎯 **虚拟 IP**: 10.0.0.x 自动分配
- 🔌 **SOCKS5 代理**: 本地 1080 端口
- 📊 **实时监控**: 节点状态、流量、延迟

## 快速开始

### 1. 安装依赖

**Linux (Ubuntu/Debian):**
```bash
sudo apt install libgl1-mesa-dev xorg-dev
```

**Windows:**
```bash
# 安装 MinGW-w64
choco install mingw
```

**macOS:**
```bash
# Xcode 已包含所有依赖
xcode-select --install
```

### 2. 编译

**Linux:**
```bash
CGO_ENABLED=1 go build -o netlink-gui
```

**Windows:**
```bash
CC=x86_64-w64-mingw32-gcc CGO_ENABLED=1 GOOS=windows go build -o netlink-gui.exe
```

**macOS:**
```bash
CGO_ENABLED=1 go build -o netlink-gui
```

**Android APK:**
```bash
# 安装 fyne CLI
go install fyne.io/fyne/v2/cmd/fyne@latest

# 安装 Android NDK
# https://developer.android.com/ndk/downloads

# 设置环境变量
export ANDROID_NDK_HOME=/path/to/android-ndk-r25c
export PATH=$PATH:$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64/bin

# 打包 APK
fyne package -os android -name "NetLink" -icon Icon.png
```

**iOS:**
```bash
# 需要 macOS + Xcode
fyne package -os ios -name "NetLink" -icon Icon.png
```

### 3. 运行

```bash
./netlink-gui
```

## 使用指南

### 连接

1. 启动程序
2. 选择传输模式（auto/udp/tcp）
3. 点击"连接"按钮
4. 程序自动分配虚拟 IP（10.0.0.x）
5. STUN 自动发现公网地址

### 信令服务器（可选）

如需通过 InfinityFree 中转：

1. 进入"设置"标签页
2. 填入信令服务器 URL（如 `https://your-site.infinityfreeapp.com/signal.php`）
3. 保存并重启

### SOCKS5 代理

连接成功后，本地启动 SOCKS5 代理：
- 地址: `127.0.0.1:1080`
- 浏览器或应用配置此代理即可访问虚拟网络

### 文件传输

1. 进入"文件"标签页
2. 输入目标虚拟 IP（如 `10.0.0.2`）
3. 选择本地文件
4. 点击发送

## 架构

```
┌─────────────────────────────────────────┐
│           Fyne GUI (main.go)            │
├─────────────────────────────────────────┤
│         Engine (engine.go)              │
│  - 状态管理、连接控制、心跳、节点发现     │
├──────────┬──────────┬───────────────────┤
│ Protocol │ Routing  │ Signal Client     │
│ (包格式) │ (路由表) │ (信令交互)         │
├──────────┴──────────┴───────────────────┤
│         Transport Layer                 │
│  ┌─────────┐  ┌─────────┐              │
│  │   UDP   │  │   TCP   │              │
│  └─────────┘  └─────────┘              │
├─────────────────────────────────────────┤
│         Crypto Layer                    │
│  X25519 + ChaCha20-Poly1305             │
├─────────────────────────────────────────┤
│         STUN / NAT Traversal            │
└─────────────────────────────────────────┘
```

## 项目结构

```
netlink-gui/
├── main.go         # GUI 界面
├── engine.go       # 核心引擎
├── protocol.go     # 网络协议
├── transport.go    # UDP/TCP 传输
├── routing.go      # 虚拟路由
├── signal.go       # 信令客户端
├── stun.go         # NAT 穿透
├── proxy.go        # SOCKS5 代理
├── crypto.go       # 加密模块
├── build.sh        # 构建脚本
└── Icon.png        # 应用图标
```

## 技术栈

- **语言**: Go 1.24+
- **GUI 框架**: Fyne v2.5.3
- **加密**: golang.org/x/crypto
- **协议**: 自定义二进制协议（21字节头）
- **传输**: UDP / TCP / 信令中继

## 跨平台编译说明

Fyne 使用 CGO 调用系统图形库（OpenGL/Vulkan），因此：

1. **必须启用 CGO**: `CGO_ENABLED=1`
2. **需要 C 编译器**: gcc / clang / mingw
3. **需要图形库**: 
   - Linux: libgl1-mesa-dev + xorg-dev
   - Windows: MinGW-w64
   - macOS: Xcode
   - Android: NDK

这也是为什么不能用 `CGO_ENABLED=0` 纯静态编译的原因。

## 构建脚本

```bash
#!/bin/bash
# build.sh - 自动化构建脚本

set -e
export PATH=$PATH:/usr/local/go/bin
export GOPROXY=https://goproxy.cn,direct

case "${1:-linux}" in
  linux)
    CGO_ENABLED=1 go build -ldflags="-s -w" -o netlink-gui
    ;;
  windows)
    apt-get install -y gcc-mingw-w64-x86-64
    CC=x86_64-w64-mingw32-gcc CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
      go build -ldflags="-s -w -H=windowsgui" -o netlink-gui.exe
    ;;
  android)
    go install fyne.io/fyne/v2/cmd/fyne@latest
    fyne package -os android -name "NetLink" -icon Icon.png
    ;;
  all)
    $0 linux && $0 windows && $0 android
    ;;
esac
```

## 常见问题

**Q: 编译报错 "build constraints exclude all Go files"**
A: 需要启用 CGO: `CGO_ENABLED=1`

**Q: 找不到 OpenGL 库**
A: Linux 安装 `libgl1-mesa-dev xorg-dev`

**Q: Windows 交叉编译失败**
A: 安装 MinGW-w64: `apt install gcc-mingw-w64-x86-64`

**Q: Android NDK 版本不对**
A: 使用 NDK r25c 或 r26b，Fyne 兼容性最好

## License

AGPL v3
