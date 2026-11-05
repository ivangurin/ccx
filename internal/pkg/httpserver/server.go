// Package httpserver — http.Server c собственным листенером (порт известен
// до старта — тесты на :0) и graceful Stop. WriteTimeout нулевой сознательно:
// SSE-стримы Claude Code живут дольше любого разумного таймаута записи.
package httpserver

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Server — обёртка http.Server с готовым листенером.
type Server struct {
	srv *http.Server
	lis net.Listener
}

// New создаёт сервер и биндит адрес (порт :0 — свободный).
func New(addr string, handler http.Handler) (*Server, error) {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("httpserver: listen %s: %w", addr, err)
	}
	srv := &http.Server{
		Handler: handler,
		// Большие контексты Claude Code едут в теле — не рвём чтение тела
		ReadTimeout: 0,
		// медленный-loris защита: заголовки обязаны прийти быстро
		ReadHeaderTimeout: 30 * time.Second,
		// SSE: таймаут записи выключен (см. комментарий пакета)
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	return &Server{srv: srv, lis: lis}, nil
}

// Addr возвращает реальный адрес листенера.
func (s *Server) Addr() string {
	return s.lis.Addr().String()
}

// Start блокирует, обслуживая соединения; возвращает nil после Stop.
func (s *Server) Start() error {
	if err := s.srv.Serve(s.lis); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("httpserver: serve: %w", err)
	}

	return nil
}

// Stop гасит сервер gracefully в пределах ctx.
func (s *Server) Stop(ctx context.Context) error {
	if err := s.srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("httpserver: shutdown: %w", err)
	}

	return nil
}
