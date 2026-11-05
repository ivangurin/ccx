// Package httpserver — http.Server with its own listener (the port is known
// before start — tests use :0) and graceful Stop. WriteTimeout is zero
// deliberately: Claude Code SSE streams outlive any reasonable write timeout.
package httpserver

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Server — an http.Server wrapper with a ready listener.
type Server struct {
	srv *http.Server
	lis net.Listener
}

// New creates the server and binds the address (port :0 — a free one).
func New(addr string, handler http.Handler) (*Server, error) {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("httpserver: listen %s: %w", addr, err)
	}
	srv := &http.Server{
		Handler: handler,
		// Large Claude Code contexts travel in the body — do not cut body reads
		ReadTimeout: 0,
		// slow-loris protection: headers must arrive quickly
		ReadHeaderTimeout: 30 * time.Second,
		// SSE: write timeout is off (see the package comment)
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	return &Server{srv: srv, lis: lis}, nil
}

// Addr returns the listener's real address.
func (s *Server) Addr() string {
	return s.lis.Addr().String()
}

// Start blocks while serving connections; returns nil after Stop.
func (s *Server) Start() error {
	if err := s.srv.Serve(s.lis); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("httpserver: serve: %w", err)
	}

	return nil
}

// Stop shuts the server down gracefully within ctx.
func (s *Server) Stop(ctx context.Context) error {
	if err := s.srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("httpserver: shutdown: %w", err)
	}

	return nil
}
