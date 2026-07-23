#!/bin/bash
# NetLink GUI - 一次编译，到处运行
set -e

echo "NetLink GUI 构建工具"
echo "===================="

PLATFORM=${1:-help}
export PATH=$PATH:/usr/local/go/bin
export GOPROXY=https://goproxy.cn,direct

case $PLATFORM in
    linux)
        echo "构建 Linux 版本..."
        CGO_ENABLED=1 go build -ldflags="-s -w" -o netlink-gui .
        chmod +x netlink-gui
        echo "✅ 完成: ./netlink-gui"
        ;;
    windows)
        echo "构建 Windows 版本..."
        CC=x86_64-w64-mingw32-gcc CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
            go build -ldflags="-s -w -H=windowsgui" -o netlink-gui.exe .
        echo "✅ 完成: netlink-gui.exe"
        ;;
    android)
        echo "构建 Android APK..."
        if command -v fyne &> /dev/null; then
            fyne package -os android -icon icon.png
            echo "✅ 完成: netlink-gui.apk"
        else
            echo "需要安装 fyne CLI: go install fyne.io/fyne/v2/cmd/fyne@latest"
            echo "然后运行: fyne package -os android -icon icon.png"
        fi
        ;;
    all)
        $0 linux
        $0 windows
        $0 android
        ;;
    *)
        echo "用法: $0 <平台>"
        echo "  linux   - Linux (需要 libgl1-mesa-dev xorg-dev)"
        echo "  windows - Windows (需要 mingw-w64)"
        echo "  android - Android APK (需要 fyne CLI + NDK)"
        echo "  all     - 全平台"
        ;;
esac
