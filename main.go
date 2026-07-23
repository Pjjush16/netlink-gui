// main.go - NetLink GUI using Fyne framework
package main

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type GUI struct {
	window     fyne.Window
	engine     *Engine
	statusLbl  *widget.Label
	infoTbl    *widget.Table
	peersTbl   *widget.Table
	logTxt     *widget.Entry
	modeSelect *widget.Select
}

func main() {
	a := app.NewWithID("io.netlink.app")
	a.Settings().SetTheme(theme.DarkTheme())
	w := a.NewWindow("NetLink - 虚拟组网")
	w.Resize(fyne.NewSize(800, 600))

	cfg, _ := LoadConfig()
	if cfg.NodeID == "" {
		cfg.NodeID = Hostname()
		cfg.Save()
	}

	engine := NewEngine(cfg)

	gui := &GUI{
		window: w,
		engine: engine,
	}

	engine.SetOnUpdate(func() {
		gui.refreshStatus()
	})

	gui.buildUI()
	gui.refreshStatus()

	// Auto-refresh
	go func() {
		for {
			time.Sleep(2 * time.Second)
			gui.refreshStatus()
		}
	}()

	w.ShowAndRun()
}

func (g *GUI) buildUI() {
	// Top bar: mode select + connect button
	g.modeSelect = widget.NewSelect([]string{"auto", "udp", "tcp"}, func(v string) {
		g.engine.Config.Mode = v
		g.engine.Config.Save()
	})
	g.modeSelect.SetSelected(g.engine.Config.Mode)

	connectBtn := widget.NewButton("连接", func() {
		if g.engine.State == StateConnected {
			g.engine.Disconnect()
			g.addLog("已断开连接")
		} else {
			g.addLog("正在连接...")
			go func() {
				err := g.engine.Connect()
				if err != nil {
					g.addLog(fmt.Sprintf("连接失败: %v", err))
				} else {
					g.addLog(fmt.Sprintf("已连接! VIP=%s 模式=%s", g.engine.Config.VirtualIP, g.engine.Transport_mode()))
				}
			}()
		}
	})

	topBar := container.NewHBox(
		widget.NewLabel("模式:"),
		g.modeSelect,
		layout.NewSpacer(),
		connectBtn,
	)

	// Status label
	g.statusLbl = widget.NewLabel("状态: 未连接")
	g.statusLbl.TextStyle = fyne.TextStyle{Bold: true}

	// Info table
	g.infoTbl = widget.NewTable(
		func() (int, int) { return 8, 2 },
		func() fyne.CanvasObject {
			return widget.NewLabel("placeholder text here")
		},
		func(id widget.TableCellID, cell fyne.CanvasObject) {
			label := cell.(*widget.Label)
			status := g.engine.GetStatus()
			keys := []string{"状态", "节点ID", "虚拟IP", "传输模式", "本地地址", "公网地址", "在线节点", "流量"}
			if id.Row < len(keys) {
				k := keys[id.Row]
				if id.Col == 0 {
					label.SetText(k)
					label.TextStyle = fyne.TextStyle{Bold: true}
				} else {
					v, ok := status[k]
					if !ok {
						v = "-"
					}
					label.SetText(v)
				}
			}
		},
	)
	g.infoTbl.SetColumnWidth(0, 120)
	g.infoTbl.SetColumnWidth(1, 400)

	// Peers table
	g.peersTbl = widget.NewTable(
		func() (int, int) {
			return len(g.engine.Peers) + 1, 4
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("placeholder")
		},
		func(id widget.TableCellID, cell fyne.CanvasObject) {
			label := cell.(*widget.Label)
			if id.Row == 0 {
				headers := []string{"节点ID", "虚拟IP", "地址", "模式"}
				if id.Col < len(headers) {
					label.SetText(headers[id.Col])
					label.TextStyle = fyne.TextStyle{Bold: true}
				}
				return
			}
			idx := id.Row - 1
			if idx < len(g.engine.Peers) {
				p := g.engine.Peers[idx]
				vals := []string{p.NodeID, p.VirtualIP, p.PublicAddr, p.Mode}
				if id.Col < len(vals) {
					label.SetText(vals[id.Col])
				}
			}
		},
	)
	g.peersTbl.SetColumnWidth(0, 150)
	g.peersTbl.SetColumnWidth(1, 120)
	g.peersTbl.SetColumnWidth(2, 200)
	g.peersTbl.SetColumnWidth(3, 80)

	// Log area
	g.logTxt = widget.NewMultiLineEntry()
	g.logTxt.Wrapping = fyne.TextWrapWord

	// Settings
	signalEntry := widget.NewEntry()
	signalEntry.SetText(g.engine.Config.SignalURL)
	signalEntry.SetPlaceHolder("信令服务器 URL (如: https://xxx.php)")

	nodeIDEntry := widget.NewEntry()
	nodeIDEntry.SetText(g.engine.Config.NodeID)

	proxyEntry := widget.NewEntry()
	proxyEntry.SetText(g.engine.Config.ProxyAddr)

	saveBtn := widget.NewButton("保存设置", func() {
		g.engine.Config.SignalURL = signalEntry.Text
		g.engine.Config.NodeID = nodeIDEntry.Text
		g.engine.Config.ProxyAddr = proxyEntry.Text
		g.engine.Config.Save()
		dialog.ShowInformation("成功", "设置已保存，重启生效", g.window)
	})

	// File transfer
	fileTarget := widget.NewEntry()
	fileTarget.SetPlaceHolder("目标虚拟IP (如: 10.0.0.2)")
	filePath := widget.NewEntry()
	filePath.SetPlaceHolder("文件路径")

	sendBtn := widget.NewButton("发送文件", func() {
		if fileTarget.Text == "" || filePath.Text == "" {
			dialog.ShowError(fmt.Errorf("请填写目标IP和文件路径"), g.window)
			return
		}
		g.addLog(fmt.Sprintf("发送文件 %s -> %s", filePath.Text, fileTarget.Text))
	})

	// Tabs
	tabs := container.NewAppTabs(
		container.NewTabItem("状态", container.NewVBox(
			g.statusLbl,
			widget.NewSeparator(),
			g.infoTbl,
		)),
		container.NewTabItem("节点", container.NewVBox(
			widget.NewLabel("在线节点列表"),
			g.peersTbl,
		)),
		container.NewTabItem("日志", g.logTxt),
		container.NewTabItem("文件", container.NewVBox(
			widget.NewLabel("目标虚拟IP:"),
			fileTarget,
			widget.NewLabel("文件路径:"),
			filePath,
			sendBtn,
		)),
		container.NewTabItem("设置", container.NewVBox(
			widget.NewLabel("节点ID:"),
			nodeIDEntry,
			widget.NewLabel("信令服务器:"),
			signalEntry,
			widget.NewLabel("SOCKS5 代理地址:"),
			proxyEntry,
			saveBtn,
		)),
	)
	tabs.SetTabLocation(container.TabLocationLeading)

	// Main layout
	content := container.NewBorder(topBar, nil, nil, nil, tabs)
	g.window.SetContent(content)
}

func (g *GUI) refreshStatus() {
	status := g.engine.GetStatus()
	state := status["状态"]
	g.statusLbl.SetText(fmt.Sprintf("状态: %s", state))
	g.infoTbl.Refresh()
	g.peersTbl.Refresh()
}

func (g *GUI) addLog(msg string) {
	timestamp := time.Now().Format("15:04:05")
	g.logTxt.SetText(g.logTxt.Text + fmt.Sprintf("[%s] %s\n", timestamp, msg))
}
