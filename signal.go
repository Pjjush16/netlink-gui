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

func (s *SignalClient) Register(vip, addr, mode string) error {
	info := NodeInfo{
		NodeID:     s.nodeID,
		VirtualIP:  vip,
		PublicAddr: addr,
		Mode:       mode,
		Online:     true,
	}
	data, _ := json.Marshal(info)
	resp, err := s.client.Post(s.serverURL+"?action=register", "application/json", bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("register failed: %d %s", resp.StatusCode, string(body))
	}
	return nil
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
	req, _ := http.NewRequest("POST", s.serverURL+"?action=heartbeat", nil)
	req.Header.Set("X-Node-ID", s.nodeID)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (s *SignalClient) Deregister() error {
	req, _ := http.NewRequest("POST", s.serverURL+"?action=deregister", nil)
	req.Header.Set("X-Node-ID", s.nodeID)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
