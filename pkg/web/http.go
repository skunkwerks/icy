package web

import (
	"context"
	"crypto/tls"
	"net/http"
	"time"

	"charm.land/log/v2"
	"github.com/skunkwerks/icy/pkg/backend"
	"github.com/skunkwerks/icy/pkg/config"
	"github.com/skunkwerks/icy/pkg/utils"
)

// HTTPServer is an http server.
type HTTPServer struct {
	ctx context.Context
	cfg *config.Config

	Server *http.Server
}

// NewHTTPServer creates a new HTTP server.
func NewHTTPServer(ctx context.Context) (*HTTPServer, error) {
	cfg := config.FromContext(ctx)
	logger := log.FromContext(ctx)
	s := &HTTPServer{
		ctx: ctx,
		cfg: cfg,
		Server: &http.Server{
			Addr:              cfg.HTTP.ListenAddr,
			Handler:           NewRouter(ctx),
			ReadHeaderTimeout: time.Second * 10,
			IdleTimeout:       time.Second * 10,
			MaxHeaderBytes:    http.DefaultMaxHeaderBytes,
			ErrorLog:          logger.StandardLog(log.StandardLogOptions{ForceLevel: log.ErrorLevel}),
		},
	}

	if cfg.Web.Enabled {
		for _, name := range reservedRepoNames(ctx) {
			logger.Warn("repository name has a segment the web UI reserves; it works over git and ssh but the web UI will not show it",
				"repo", name, "reserved", utils.ReservedRepoSegments)
		}
	}

	return s, nil
}

// reservedRepoNames returns the existing repositories whose names have a
// reserved segment. They were created before the segment was reserved.
func reservedRepoNames(ctx context.Context) []string {
	be := backend.FromContext(ctx)
	if be == nil {
		return nil
	}
	repos, err := be.Repositories(ctx)
	if err != nil {
		log.FromContext(ctx).Error("failed to list repositories", "err", err)
		return nil
	}
	var names []string
	for _, r := range repos {
		if utils.HasReservedSegment(r.Name()) {
			names = append(names, r.Name())
		}
	}
	return names
}

// SetTLSConfig sets the TLS configuration for the HTTP server.
func (s *HTTPServer) SetTLSConfig(tlsConfig *tls.Config) {
	s.Server.TLSConfig = tlsConfig
}

// Close closes the HTTP server.
func (s *HTTPServer) Close() error {
	return s.Server.Close()
}

// ListenAndServe starts the HTTP server.
func (s *HTTPServer) ListenAndServe() error {
	if s.Server.TLSConfig != nil {
		return s.Server.ListenAndServeTLS("", "")
	}
	return s.Server.ListenAndServe()
}

// Shutdown gracefully shuts down the HTTP server.
func (s *HTTPServer) Shutdown(ctx context.Context) error {
	return s.Server.Shutdown(ctx)
}
