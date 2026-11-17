package web

import (
	"context"
	"net/http"

	"charm.land/log/v2"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/skunkwerks/icy/pkg/config"
)

// NewRouter returns a new HTTP router.
func NewRouter(ctx context.Context) http.Handler {
	logger := log.FromContext(ctx).WithPrefix("http")
	router := mux.NewRouter()

	cfg := config.FromContext(ctx)

	// Health routes
	HealthController(ctx, router)

	// Git routes
	GitController(ctx, router)

	// The web UI takes what the git, LFS and go-get routes leave.
	if cfg.Web.Enabled {
		newWebUI().register(router)
	}

	router.PathPrefix("/").HandlerFunc(renderNotFound)

	// Context handler
	// Adds context to the request
	h := NewLoggingMiddleware(router, logger)
	h = NewContextHandler(ctx)(h)
	h = handlers.RecoveryHandler()(h)

	h = handlers.CORS(handlers.AllowedHeaders(cfg.HTTP.CORS.AllowedHeaders),
		handlers.AllowedOrigins(cfg.HTTP.CORS.AllowedOrigins),
		handlers.AllowedMethods(cfg.HTTP.CORS.AllowedMethods),
	)(h)

	return h
}
