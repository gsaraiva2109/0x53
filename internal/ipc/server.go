package ipc

import (
	"log"
	"net"
	"net/rpc"
	"os"

	"adblock/internal/config"
	"adblock/internal/core"
)

// --- IPC Types ---

type Void struct{}

type StatsReply struct {
	QueriesTotal   int
	QueriesBlocked int
	ActiveRules    int
}

type ToggleArgs struct {
	Name    string
	Enabled bool
}

type LogArgs struct {
	Count int
}

type LogReply struct {
	Lines []string
}

// --- RPC Server Adapter ---

// RPCServer exposes AppService methods via net/rpc compatible signature.
type RPCServer struct {
	svc core.Service
}

func (s *RPCServer) GetStats(args *Void, reply *StatsReply) error {
	q, b, r, err := s.svc.GetStats()
	reply.QueriesTotal = q
	reply.QueriesBlocked = b
	reply.ActiveRules = r
	return err
}

func (s *RPCServer) ListSources(args *Void, reply *[]config.BlocklistSource) error {
	srcs, err := s.svc.ListSources()
	*reply = srcs
	return err
}

func (s *RPCServer) ToggleSource(args *ToggleArgs, reply *Void) error {
	return s.svc.ToggleSource(args.Name, args.Enabled)
}

func (s *RPCServer) Reload(args *Void, reply *Void) error {
	return s.svc.Reload()
}

func (s *RPCServer) GetRecentLogs(args *LogArgs, reply *LogReply) error {
	lines, err := s.svc.GetRecentLogs(args.Count)
	reply.Lines = lines
	return err
}

// StartServer starts the Unix Domain Socket listener.
// It runs in a goroutine until context is cancelled or listener closed.
// returns the listener so it can be closed on shutdown.
func StartServer(svc core.Service, socketPath string) (net.Listener, error) {
	rpcObj := &RPCServer{svc: svc}
	server := rpc.NewServer()
	if err := server.RegisterName("Sinkhole", rpcObj); err != nil {
		return nil, err
	}

	// Clean up old socket
	if _, err := os.Stat(socketPath); err == nil {
		if err := os.Remove(socketPath); err != nil {
			return nil, err
		}
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}

	// Set permissions (rw-rw-rw-) allowing any user to connect (TUI client)
	if err := os.Chmod(socketPath, 0666); err != nil {
		log.Printf("Warning: Failed to set socket permissions: %v", err)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.ServeConn(conn)
		}
	}()

	return listener, nil
}
