package ipc

import (
	"net/rpc"

	"0x53/internal/config"
	"0x53/internal/core"
)

// Client implements core.Service via RPC.
type Client struct {
	client *rpc.Client
}

// NewClient connects to the unix socket.
func NewClient(socketPath string) (*Client, error) {
	c, err := rpc.Dial("unix", socketPath)
	if err != nil {
		return nil, err
	}
	return &Client{client: c}, nil
}

func (c *Client) Close() error {
	return c.client.Close()
}

// --- Service Implementation ---

func (c *Client) GetStats() (int, int, int, error) {
	var reply StatsReply
	err := c.client.Call("Sinkhole.GetStats", &Void{}, &reply)
	return reply.QueriesTotal, reply.QueriesBlocked, reply.ActiveRules, err
}

func (c *Client) ListSources() ([]config.BlocklistSource, error) {
	var reply []config.BlocklistSource
	err := c.client.Call("Sinkhole.ListSources", &Void{}, &reply)
	return reply, err
}

func (c *Client) ToggleSource(name string, enabled bool) error {
	args := ToggleArgs{Name: name, Enabled: enabled}
	return c.client.Call("Sinkhole.ToggleSource", &args, &Void{})
}

func (c *Client) Reload() error {
	return c.client.Call("Sinkhole.Reload", &Void{}, &Void{})
}

func (c *Client) GetRecentLogs(count int) ([]string, error) {
	args := LogArgs{Count: count}
	var reply LogReply
	err := c.client.Call("Sinkhole.GetRecentLogs", &args, &reply)
	return reply.Lines, err
}

func (c *Client) AddAllowed(domain string) error {
	args := AllowlistArgs{Domain: domain}
	return c.client.Call("Sinkhole.AddAllowed", &args, &Void{})
}

func (c *Client) RemoveAllowed(domain string) error {
	args := AllowlistArgs{Domain: domain}
	return c.client.Call("Sinkhole.RemoveAllowed", &args, &Void{})
}

func (c *Client) ListAllowed() ([]string, error) {
	var reply []string
	err := c.client.Call("Sinkhole.ListAllowed", &Void{}, &reply)
	return reply, err
}

// Local Records
func (c *Client) AddLocalRecord(domain, ip string) error {
	args := LocalRecordArgs{Domain: domain, IP: ip}
	return c.client.Call("Sinkhole.AddLocalRecord", &args, &Void{})
}

func (c *Client) RemoveLocalRecord(domain string) error {
	args := LocalRecordArgs{Domain: domain}
	return c.client.Call("Sinkhole.RemoveLocalRecord", &args, &Void{})
}

func (c *Client) ListLocalRecords() (map[string]string, error) {
	var reply map[string]string
	err := c.client.Call("Sinkhole.ListLocalRecords", &Void{}, &reply)
	return reply, err
}

// Ensure interface compliance
var _ core.Service = (*Client)(nil)
