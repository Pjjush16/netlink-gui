// routing.go - Virtual IP routing table
package main

import (
	"fmt"
	"net"
	"sync"
	"time"
)

type Route struct {
	VirtualIP net.IP
	PeerAddr  net.Addr
	NodeID    string
	LastSeen  time.Time
	Direct    bool
}

type Router struct {
	routes map[string]*Route
	mu     sync.RWMutex
}

func NewRouter() *Router {
	return &Router{routes: make(map[string]*Route)}
}

func (r *Router) Add(vip net.IP, addr net.Addr, nodeID string, direct bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes[vip.To4().String()] = &Route{
		VirtualIP: vip.To4(),
		PeerAddr:  addr,
		NodeID:    nodeID,
		LastSeen:  time.Now(),
		Direct:    direct,
	}
}

func (r *Router) Lookup(vip net.IP) (*Route, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rt, ok := r.routes[vip.To4().String()]
	if !ok {
		return nil, false
	}
	if time.Since(rt.LastSeen) > 5*time.Minute {
		return nil, false
	}
	return rt, true
}

func (r *Router) Remove(vip net.IP) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.routes, vip.To4().String())
}

func (r *Router) All() []*Route {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []*Route
	for _, rt := range r.routes {
		if time.Since(rt.LastSeen) <= 5*time.Minute {
			result = append(result, rt)
		}
	}
	return result
}

func (r *Router) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c := 0
	for _, rt := range r.routes {
		if time.Since(rt.LastSeen) <= 5*time.Minute {
			c++
		}
	}
	return c
}

func (r *Router) AllocateVIP() (net.IP, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := 1; i <= 254; i++ {
		ip := net.IPv4(10, 0, 0, byte(i))
		if _, ok := r.routes[ip.To4().String()]; !ok {
			return ip, nil
		}
	}
	return nil, fmt.Errorf("no available IPs")
}

func (r *Router) String() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s := fmt.Sprintf("%-15s %-25s %-15s %s\n", "VirtualIP", "Peer", "Node", "Direct")
	for _, rt := range r.routes {
		s += fmt.Sprintf("%-15s %-25s %-15s %v\n", rt.VirtualIP, rt.PeerAddr, rt.NodeID, rt.Direct)
	}
	return s
}
