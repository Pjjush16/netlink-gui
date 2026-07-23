# NetLink GUI - 构建状态报告

## 已完成

### 1. Linux 版本 ✅
- 文件: `netlink-gui` (23MB, ELF 64-bit x86-64)
- 状态: 编译成功，可直接运行
- 依赖: 需要 `libgl1-mesa` 和 `libx11` 运行时库

### 2. 源代码 ✅
- 完整的 Fyne GUI 项目
- 所有模块: protocol, transport, crypto, stun, routing, signal, proxy, engine, main
- 构建脚本: `build.sh`
- 应用图标: `Icon.png`

### 3. 文档 ✅
- README.md: 完整的使用指南
- 跨平台编译说明
- 常见问题解答

## 待完成（需要本地环境）

### Windows 版本
需要 MinGW-w64 工具链，建议在有完整工具链的机器上编译：

```bash
# 安装 MinGW-w64
apt install gcc-mingw-w64-x86-64

# 编译
CC=x86_64-w64-mingw32-gcc CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
  go build -ldflags="-s -w -H=windowsgui" -o netlink-gui.exe
```

### Android APK
需要 Android NDK r25c 或更高版本：

```bash
# 1. 安装 fyne CLI
go install fyne.io/fyne/v2/cmd/fyne@latest

# 2. 下载 Android NDK
# https://developer.android.com/ndk/downloads
# 下载 android-ndk-r25c-linux.zip 并解压

# 3. 设置环境变量
export ANDROID_NDK_HOME=/path/to/android-ndk-r25c
export PATH=$PATH:$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64/bin

# 4. 打包 APK
fyne package -os android -name "NetLink" -icon Icon.png
```

### macOS 版本
需要 macOS + Xcode：

```bash
CGO_ENABLED=1 go build -o netlink-gui
```

### iOS 版本
需要 macOS + Xcode：

```bash
fyne package -os ios -name "NetLink" -icon Icon.png
```

## 为什么不能纯静态编译？

Fyne 使用 CGO 调用系统图形 API：
- Linux: OpenGL (libGL)
- Windows: DirectX / OpenGL
- macOS: Metal / OpenGL
- Android: OpenGL ES
- iOS: Metal

这些系统库无法静态链接，必须通过 CGO 动态调用。

**解决方案**：
1. 在有完整工具链的机器上编译各平台版本
2. 使用 fyne-cross 工具（需要 Docker）
3. 使用 CI/CD 自动化构建（GitHub Actions 等）

## 推荐的构建流程

### 方案 1: 本地编译（推荐）

在你的开发机器上：

```bash
# 1. 克隆项目
cd netlink-gui

# 2. 安装依赖
# Linux: sudo apt install libgl1-mesa-dev xorg-dev
# Windows: choco install mingw
# macOS: xcode-select --install

# 3. 编译当前平台
./build.sh linux  # 或 windows / android

# 4. 运行测试
./netlink-gui
```

### 方案 2: fyne-cross（需要 Docker）

```bash
# 安装 fyne-cross
go install github.com/fyne-io/fyne-cross@latest

# 编译所有平台
fyne-cross linux
fyne-cross windows
fyne-cross darwin
fyne-cross android
fyne-cross ios
```

### 方案 3: CI/CD 自动化

创建 `.github/workflows/build.yml`：

```yaml
name: Build NetLink GUI
on: [push, release]

jobs:
  build:
    strategy:
      matrix:
        os: [ubuntu-latest, windows-latest, macos-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
        with:
          go-version: '1.24'
      
      - name: Install dependencies (Linux)
        if: runner.os == 'Linux'
        run: sudo apt install -y libgl1-mesa-dev xorg-dev
      
      - name: Build
        run: |
          CGO_ENABLED=1 go build -o netlink-gui
      
      - name: Upload artifact
        uses: actions/upload-artifact@v3
        with:
          name: netlink-gui-${{ runner.os }}
          path: netlink-gui*
```

## 项目文件清单

```
netlink-gui/
├── README.md           # 使用指南
├── BUILD_STATUS.md     # 本文档
├── build.sh            # 构建脚本
├── go.mod              # Go 模块定义
├── Icon.png            # 应用图标
├── main.go             # GUI 界面
├── engine.go           # 核心引擎
├── protocol.go         # 网络协议
├── transport.go        # UDP/TCP 传输
├── routing.go          # 虚拟路由
├── signal.go           # 信令客户端
├── stun.go             # NAT 穿透
├── proxy.go            # SOCKS5 代理
├── crypto.go           # 加密模块
└── netlink-gui         # Linux 二进制 (23MB)
```

## 下一步建议

1. **本地编译**: 在你的开发机器上安装完整工具链，编译各平台版本
2. **测试功能**: 运行 Linux 版本，测试连接、节点发现、文件传输等功能
3. **部署信令服务器**: 将 `signal.php` 部署到 InfinityFree 或自建服务器
4. **多设备测试**: 在两台设备上运行 NetLink，验证 P2P 连接
5. **打包发布**: 编译所有平台版本，创建 Release

## 技术亮点

- ✅ 真正的跨平台（桌面 + 移动）
- ✅ 无服务器 P2P 架构
- ✅ 端到端加密
- ✅ 自动 NAT 穿透
- ✅ 虚拟 IP 网络
- ✅ SOCKS5 代理
- ✅ 文件传输
- ✅ 实时监控

## 已知限制

1. **CGO 依赖**: 需要 C 编译器和系统图形库
2. **首次启动**: 需要 STUN 发现公网地址（可能被防火墙阻止）
3. **NAT 穿透**: 对称 NAT 环境下可能需要中继服务器
4. **移动端**: 后台运行时连接可能断开

## 联系方式

如有问题，请查看 README.md 或提交 Issue。
