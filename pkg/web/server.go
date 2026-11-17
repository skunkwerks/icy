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

	// Web UI routes that must come before the git routes: the front page
	// and anything with a "~" path segment, which no repository name has.
	var ui *webUI
	if cfg.Web.Enabled {
		ui = newWebUI()
		ui.registerFront(router)
	}

	// Git routes
	GitController(ctx, router)

	// The repository summary takes whatever GET the git and go-get routes
	// leave.
	if ui != nil {
		ui.registerSummary(router)
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
