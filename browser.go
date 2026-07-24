// browser.go - Built-in web browser for virtual network
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

type Browser struct {
	window      fyne.Window
	engine      *Engine
	urlEntry    *widget.Entry
	content     *widget.RichText
	statusLabel *widget.Label
	backBtn     *widget.Button
	forwardBtn  *widget.Button
	history     []string
	historyIdx  int
	client      *http.Client
}

func NewBrowser(w fyne.Window, e *Engine) *Browser {
	b := &Browser{
		window: w,
		engine: e,
		history: []string{},
		historyIdx: -1,
	}
	
	// Create HTTP client with timeout
	b.client = &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			// Use SOCKS5 proxy to access virtual network
			DialContext: b.dialVirtualNetwork,
		},
	}
	
	return b
}

func (b *Browser) dialVirtualNetwork(ctx context.Context, network, addr string) (net.Conn, error) {
	// Parse address to check if it's a virtual IP
	parts := strings.Split(addr, ":")
	if len(parts) >= 1 {
		ip := parts[0]
		// Check if it's a virtual IP (10.0.0.x)
		if strings.HasPrefix(ip, "10.0.0.") {
			// Route through SOCKS5 proxy
			proxyAddr := b.engine.Config.ProxyAddr
			conn, err := net.Dial("tcp", proxyAddr)
			if err != nil {
				return nil, fmt.Errorf("connect to SOCKS5 proxy: %w", err)
			}
			
			// SOCKS5 handshake
			conn.Write([]byte{0x05, 0x01, 0x00})
			buf := make([]byte, 2)
			conn.Read(buf)
			
			// Connect request
			host := ip
			port := 80
			if len(parts) >= 2 {
				fmt.Sscanf(parts[1], "%d", &port)
			}
			
			hostBytes := []byte(host)
			req := []byte{0x05, 0x01, 0x00, 0x03}
			req = append(req, byte(len(hostBytes)))
			req = append(req, hostBytes...)
			req = append(req, byte(port>>8), byte(port))
			
			conn.Write(req)
			resp := make([]byte, 10)
			conn.Read(resp)
			
			if resp[1] != 0x00 {
				conn.Close()
				return nil, fmt.Errorf("SOCKS5 connect failed")
			}
			
			return conn, nil
		}
	}
	
	// Regular connection for non-virtual IPs
	return net.Dial(network, addr)
}

func (b *Browser) CreateTab() fyne.CanvasObject {
	// URL bar
	b.urlEntry = widget.NewEntry()
	b.urlEntry.SetPlaceHolder("输入虚拟网络地址 (如: http://10.0.0.2:8080)")
	b.urlEntry.OnSubmitted = func(url string) {
		b.Navigate(url)
	}
	
	goBtn := widget.NewButton("访问", func() {
		b.Navigate(b.urlEntry.Text)
	})
	
	// Navigation buttons
	b.backBtn = widget.NewButton("←", func() {
		b.Back()
	})
	b.backBtn.Disable()
	
	b.forwardBtn = widget.NewButton("→", func() {
		b.Forward()
	})
	b.forwardBtn.Disable()
	
	refreshBtn := widget.NewButton("↻", func() {
		if len(b.history) > 0 && b.historyIdx >= 0 {
			b.Navigate(b.history[b.historyIdx])
		}
	})
	
	navBar := container.NewHBox(b.backBtn, b.forwardBtn, refreshBtn)
	
	urlBar := container.NewBorder(nil, nil, navBar, goBtn, b.urlEntry)
	
	// Content area
	b.content = widget.NewRichTextFromMarkdown("# NetLink 内嵌浏览器\n\n在地址栏输入虚拟网络上的 HTTP 服务地址\n\n例如：\n- `http://10.0.0.2:8080`\n- `http://10.0.0.3:3000`")
	b.content.Wrapping = fyne.TextWrapWord
	
	scroll := container.NewVScroll(b.content)
	
	// Status bar
	b.statusLabel = widget.NewLabel("就绪")
	
	// Quick links
	quickLinks := container.NewHBox(
		widget.NewButton("10.0.0.1:80", func() {
			b.urlEntry.SetText("http://10.0.0.1:80")
			b.Navigate("http://10.0.0.1:80")
		}),
		widget.NewButton("10.0.0.2:80", func() {
			b.urlEntry.SetText("http://10.0.0.2:80")
			b.Navigate("http://10.0.0.2:80")
		}),
		widget.NewButton("10.0.0.2:8080", func() {
			b.urlEntry.SetText("http://10.0.0.2:8080")
			b.Navigate("http://10.0.0.2:8080")
		}),
	)
	
	topBar := container.NewVBox(urlBar, quickLinks)
	
	return container.NewBorder(topBar, b.statusLabel, nil, nil, scroll)
}

func (b *Browser) Navigate(url string) {
	if url == "" {
		return
	}
	
	// Add http:// if missing
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "http://" + url
		b.urlEntry.SetText(url)
	}
	
	b.statusLabel.SetText(fmt.Sprintf("正在加载 %s ...", url))
	b.content.ParseMarkdown(fmt.Sprintf("# 正在加载\n\n%s\n\n请稍候...", url))
	
	// Fetch page
	go func() {
		resp, err := b.client.Get(url)
		if err != nil {
			b.content.ParseMarkdown(fmt.Sprintf("# ❌ 加载失败\n\n**URL:** %s\n\n**错误:** %s\n\n**提示:**\n- 确保目标虚拟 IP 已连接\n- 确保目标服务正在运行\n- 检查 SOCKS5 代理是否正常", url, err.Error()))
			b.statusLabel.SetText("加载失败")
			return
		}
		defer resp.Body.Close()
		
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			b.content.ParseMarkdown(fmt.Sprintf("# ❌ 读取失败\n\n%s", err.Error()))
			b.statusLabel.SetText("读取失败")
			return
		}
		
		// Convert HTML to markdown (simple conversion)
		content := b.htmlToMarkdown(string(body))
		
		b.content.ParseMarkdown(fmt.Sprintf("# %s\n\n%s", url, content))
		b.statusLabel.SetText(fmt.Sprintf("✅ 已加载 (%d bytes, HTTP %d)", len(body), resp.StatusCode))
		
		// Add to history
		if b.historyIdx < 0 || b.history[b.historyIdx] != url {
			// Remove forward history
			b.history = b.history[:b.historyIdx+1]
			b.history = append(b.history, url)
			b.historyIdx = len(b.history) - 1
		}
		
		b.updateNavButtons()
	}()
}

func (b *Browser) Back() {
	if b.historyIdx > 0 {
		b.historyIdx--
		b.urlEntry.SetText(b.history[b.historyIdx])
		b.Navigate(b.history[b.historyIdx])
		b.updateNavButtons()
	}
}

func (b *Browser) Forward() {
	if b.historyIdx < len(b.history)-1 {
		b.historyIdx++
		b.urlEntry.SetText(b.history[b.historyIdx])
		b.Navigate(b.history[b.historyIdx])
		b.updateNavButtons()
	}
}

func (b *Browser) updateNavButtons() {
	if b.historyIdx > 0 {
		b.backBtn.Enable()
	} else {
		b.backBtn.Disable()
	}
	
	if b.historyIdx < len(b.history)-1 {
		b.forwardBtn.Enable()
	} else {
		b.forwardBtn.Disable()
	}
}

func (b *Browser) htmlToMarkdown(html string) string {
	// Simple HTML to Markdown conversion
	// This is a basic implementation - can be enhanced
	
	// Remove script and style tags
	re := regexp.MustCompile(`(?s)<script[^>]*>.*?</script>`)
	html = re.ReplaceAllString(html, "")
	re = regexp.MustCompile(`(?s)<style[^>]*>.*?</style>`)
	html = re.ReplaceAllString(html, "")
	
	// Convert headers
	re = regexp.MustCompile(`<h1[^>]*>(.*?)</h1>`)
	html = re.ReplaceAllString(html, "# $1\n\n")
	re = regexp.MustCompile(`<h2[^>]*>(.*?)</h2>`)
	html = re.ReplaceAllString(html, "## $1\n\n")
	re = regexp.MustCompile(`<h3[^>]*>(.*?)</h3>`)
	html = re.ReplaceAllString(html, "### $1\n\n")
	
	// Convert paragraphs and breaks
	re = regexp.MustCompile(`<p[^>]*>(.*?)</p>`)
	html = re.ReplaceAllString(html, "$1\n\n")
	re = regexp.MustCompile(`<br\s*/?>`)
	html = re.ReplaceAllString(html, "\n")
	
	// Convert bold and italic
	re = regexp.MustCompile(`<(strong|b)>(.*?)</\1>`)
	html = re.ReplaceAllString(html, "**$2**")
	re = regexp.MustCompile(`<(em|i)>(.*?)</\1>`)
	html = re.ReplaceAllString(html, "*$2*")
	
	// Convert links
	re = regexp.MustCompile(`<a[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	html = re.ReplaceAllString(html, "[$2]($1)")
	
	// Remove remaining HTML tags
	re = regexp.MustCompile(`<[^>]+>`)
	html = re.ReplaceAllString(html, "")
	
	// Decode HTML entities
	html = strings.ReplaceAll(html, "&nbsp;", " ")
	html = strings.ReplaceAll(html, "&lt;", "<")
	html = strings.ReplaceAll(html, "&gt;", ">")
	html = strings.ReplaceAll(html, "&amp;", "&")
	html = strings.ReplaceAll(html, "&quot;", "\"")
	
	// Clean up whitespace
	re = regexp.MustCompile(`\n{3,}`)
	html = re.ReplaceAllString(html, "\n\n")
	
	return strings.TrimSpace(html)
}
