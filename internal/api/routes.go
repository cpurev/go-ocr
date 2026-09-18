package api

import (
	"crypto/subtle"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/cpurev/go-ocr/internal/httpx"
)

type route struct {
	method  string
	path    string
	handler http.HandlerFunc

	// private routes need the API token. The service must be public for Meta
	// to reach the webhook, so without this anyone holding the run.app URL
	// could read every receipt, phone numbers included, or spend OCR time.
	private bool
}

func (s *Server) routeTable() []route {
	return []route{
		{http.MethodGet, "/healthz", s.handleHealth, false},
		{http.MethodGet, "/readyz", s.handleReady, false},

		{http.MethodPost, "/api/v1/scan", s.handleScan, true},

		// The webhook authenticates itself: verify token and HMAC signature.
		{http.MethodGet, "/api/v1/whatsapp/webhook", s.handleWebhookVerify, false},
		{http.MethodPost, "/api/v1/whatsapp/webhook", s.handleWebhookReceive, false},

		{http.MethodGet, "/api/v1/receipts", s.handleListReceipts, true},
		{http.MethodPost, "/api/v1/receipts", s.handleCreateReceipt, true},
		{http.MethodGet, "/api/v1/receipts/{id}", s.handleGetReceipt, true},
	}
}

func (s *Server) Routes() http.Handler {
	table := s.routeTable()

	mux := http.NewServeMux()

	pathsOnly := http.NewServeMux()
	registered := make(map[string]bool)

	for _, rt := range table {
		handler := rt.handler
		if rt.private {
			handler = s.requireToken(handler)
		}
		mux.HandleFunc(rt.method+" "+rt.path, handler)

		if !registered[rt.path] {
			registered[rt.path] = true

			pathsOnly.Handle(rt.path, http.NotFoundHandler())
		}
	}

	mux.Handle("/", s.handleUnmatched(table, pathsOnly))

	return httpx.Chain(mux,
		httpx.RequestID,
		httpx.Recoverer(s.logger),
	)
}

// Handler is Routes behind a request timeout. The logger sits outside the
// timeout so it records what the client got: inside, a timed-out request was
// logged with whatever the handler wrote into the discarded buffer, usually
// 200, while Meta actually received a 503 and retried. RequestID runs first so
// the log line and the handler share one id.
func (s *Server) Handler(timeout time.Duration, timeoutBody string) http.Handler {
	return httpx.Chain(http.TimeoutHandler(s.Routes(), timeout, timeoutBody),
		httpx.RequestID,
		httpx.Logger(s.logger),
	)
}

// requireToken admits a request carrying "Authorization: Bearer <API_TOKEN>".
// With no API_TOKEN configured the private routes are closed to everyone,
// so forgetting the variable fails safe instead of open.
func (s *Server) requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if s.cfg.APIToken == "" || !ok ||
			subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.APIToken)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="go-ocr"`)
			httpx.Error(w, r, http.StatusUnauthorized, "a valid API token is required", nil)
			return
		}
		next(w, r)
	}
}

func (s *Server) handleUnmatched(table []route, pathsOnly *http.ServeMux) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := pathsOnly.Handler(r); pattern != "" {
			allowed := allowedMethods(table, pattern)

			w.Header().Set("Allow", strings.Join(allowed, ", "))
			httpx.Error(w, r, http.StatusMethodNotAllowed,
				"method "+r.Method+" is not allowed for this resource", nil)
			return
		}

		httpx.Error(w, r, http.StatusNotFound, "the requested resource was not found", nil)
	}
}

func allowedMethods(table []route, path string) []string {
	methods := make([]string, 0, 4)
	for _, rt := range table {
		if rt.path == path && !slices.Contains(methods, rt.method) {
			methods = append(methods, rt.method)
		}
	}
	slices.Sort(methods)
	return methods
}
