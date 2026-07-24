// signal.go - Signaling server client
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
	Mode       string `json:"mode"`
	Online     bool   `json:"online"`
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
		Status    string `json:"status"`
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
	req, _ := http.NewRequest("POST", s.serverURL+"?action=heartbeat", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (s *SignalClient) Deregister() error {
	info := map[string]string{"node_id": s.nodeID}
	data, _ := json.Marshal(info)
	req, _ := http.NewRequest("POST", s.serverURL+"?action=deregister", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
