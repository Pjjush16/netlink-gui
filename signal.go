// signal.go - Signaling server client with relay support
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type NodeInfo struct {
	NodeID     string `json:"node_id"`
	VirtualIP  string `json:"virtual_ip"`
	PublicAddr string `json:"public_addr"`
	RealAddr   string `json:"real_addr"`
	Mode       string `json:"mode"`
	Online     bool   `json:"online"`
}

type RelayMessage struct {
	From string `json:"from"`
	Data string `json:"data"` // base64 encoded
	Time int64  `json:"time"`
}

type RelayPollResponse struct {
	Status   string         `json:"status"`
	Messages []RelayMessage `json:"messages"`
	Count    int            `json:"count"`
}

type SignalClient struct {
	serverURL string
	nodeID    string
	client    *http.Client
}

func NewSignalClient(url, nodeID string) *SignalClient {
	return &SignalClient{
		serverURL: url,
		nodeID:    nodeID,
		client:    &http.Client{Timeout: 15 * time.Second},
	}
}

func (s *SignalClient) Register(addr, mode string) (string, error) {
	info := map[string]string{
		"node_id":     s.nodeID,
		"public_addr": addr,
		"mode":        mode,
	}
	data, _ := json.Marshal(info)
	resp, err := s.client.Post(s.serverURL+"?action=register", "application/json", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("register failed: %d %s", resp.StatusCode, string(body))
	}

	var result struct {
		VirtualIP string `json:"virtual_ip"`
	}
	json.Unmarshal(body, &result)

	if result.VirtualIP == "" {
		return "", fmt.Errorf("server did not return virtual_ip")
	}

	return result.VirtualIP, nil
}

func (s *SignalClient) Lookup(vip string) (*NodeInfo, error) {
	resp, err := s.client.Get(fmt.Sprintf("%s?action=lookup&virtual_ip=%s", s.serverURL, vip))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("not found: %s", vip)
	}
	body, _ := io.ReadAll(resp.Body)
	var info NodeInfo
	json.Unmarshal(body, &info)
	return &info, nil
}

func (s *SignalClient) List() ([]NodeInfo, error) {
	resp, err := s.client.Get(s.serverURL + "?action=list")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var nodes []NodeInfo
	json.Unmarshal(body, &nodes)
	return nodes, nil
}

func (s *SignalClient) Heartbeat() error {
	info := map[string]string{"node_id": s.nodeID}
	data, _ := json.Marshal(info)
	resp, err := s.client.Post(s.serverURL+"?action=heartbeat", "application/json", bytes.NewReader(data))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (s *SignalClient) Deregister() error {
	info := map[string]string{"node_id": s.nodeID}
	data, _ := json.Marshal(info)
	resp, err := s.client.Post(s.serverURL+"?action=deregister", "application/json", bytes.NewReader(data))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Relay sends data to a peer through the signal server.
func (s *SignalClient) Relay(toNodeID, base64Data string) error {
	msg := map[string]string{
		"from": s.nodeID,
		"to":   toNodeID,
		"data": base64Data,
	}
	data, _ := json.Marshal(msg)
	resp, err := s.client.Post(s.serverURL+"?action=relay", "application/json", bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("relay failed: %d %s", resp.StatusCode, string(body))
	}
	return nil
}

// RelayPoll checks for incoming relay messages.
func (s *SignalClient) RelayPoll() ([]RelayMessage, error) {
	url := fmt.Sprintf("%s?action=relay_poll&node_id=%s", s.serverURL, s.nodeID)
	resp, err := s.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var result RelayPollResponse
	json.Unmarshal(body, &result)
	return result.Messages, nil
}
