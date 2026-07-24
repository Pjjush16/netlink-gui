// cli_main.go - CLI entry point (no GUI dependency)
//go:build ignore

package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("NetLink CLI")
		fmt.Println("  init          - Initialize config")
		fmt.Println("  up [--mode X] - Start virtual network")
		fmt.Println("  status        - Show status")
		fmt.Println("  peers         - List peers")
		fmt.Println("  down          - Stop")
		os.Exit(1)
	}

	cmd := os.Args[1]

	switch cmd {
	case "init":
		cfg := DefaultConfig()
		hostname, _ := os.Hostname()
		if hostname == "" {
			hostname = "node"
		}
		cfg.NodeID = hostname
		kp, err := GenerateKeyPair()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Key generation error: %v\n", err)
			os.Exit(1)
		}
		cfg.PrivateKey = fmt.Sprintf("%x", kp.PrivateKey)
		cfg.Save()
		fmt.Println("NetLink initialized!")
		fmt.Printf("  Node ID: %s\n", cfg.NodeID)
		fmt.Printf("  Config: %s\n", ConfigPath())

	case "up":
		mode := "auto"
		for i, arg := range os.Args {
			if arg == "--mode" && i+1 < len(os.Args) {
				mode = os.Args[i+1]
			}
		}

		cfg, _ := LoadConfig()
		cfg.Mode = mode
		cfg.Save()

		engine := NewEngine(cfg)
		engine.SetOnUpdate(func() {
			status := engine.GetStatus()
			fmt.Printf("[%s] State=%s VIP=%s Peers=%s\n",
				time.Now().Format("15:04:05"),
				status["状态"],
				status["虚拟IP"],
				status["在线节点"])
		})

		fmt.Println("Starting NetLink...")
		err := engine.Connect()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Connect failed: %v\n", err)
			os.Exit(1)
		}

		// Print full status
		status := engine.GetStatus()
		fmt.Println()
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		for k, v := range status {
			fmt.Printf("  %-10s %s\n", k+":", v)
		}
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

		// Wait for signal
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

		// Periodic status
		go func() {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					s := engine.GetStatus()
					fmt.Printf("[%s] VIP=%s Peers=%s Traffic=%s\n",
						time.Now().Format("15:04:05"),
						s["虚拟IP"],
						s["在线节点"],
						s["流量"])
				}
			}
		}()

		<-sigCh
		fmt.Println("\nShutting down...")
		engine.Disconnect()
		fmt.Println("Stopped.")

	case "status":
		cfg, _ := LoadConfig()
		fmt.Println("NetLink Config:")
		fmt.Printf("  Node ID:    %s\n", cfg.NodeID)
		fmt.Printf("  Virtual IP: %s\n", cfg.VirtualIP)
		fmt.Printf("  Mode:       %s\n", cfg.Mode)
		fmt.Printf("  Signal:     %s\n", cfg.SignalURL)
		fmt.Printf("  Listen:     %s\n", cfg.ListenAddr)
		fmt.Printf("  Proxy:      %s\n", cfg.ProxyAddr)

	case "peers":
		cfg, _ := LoadConfig()
		if cfg.SignalURL == "" {
			fmt.Println("No signal server configured")
			return
		}
		sig := NewSignalClient(cfg.SignalURL, cfg.NodeID)
		peers, err := sig.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if len(peers) == 0 {
			fmt.Println("No peers online")
			return
		}
		fmt.Printf("%-20s %-15s %-25s %-8s\n", "Node ID", "Virtual IP", "Address", "Mode")
		fmt.Println("────────────────────────────────────────────────────────────────")
		for _, p := range peers {
			online := "❌"
			if p.Online {
				online = "✅"
			}
			fmt.Printf("%-20s %-15s %-25s %-8s %s\n", p.NodeID, p.VirtualIP, p.PublicAddr, p.Mode, online)
		}

	case "down":
		fmt.Println("NetLink stopped")

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		os.Exit(1)
	}
}
