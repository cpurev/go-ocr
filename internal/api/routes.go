package api

import (
	"net/http"
	"slices"
	"strings"

	"github.com/cpurev/go-ocr/internal/httpx"
)

type route struct {
	method  string
	path    string
	handler http.HandlerFunc
}

func (s *Server) routeTable() []route {
	return []route{
		{http.MethodGet, "/healthz", s.handleHealth},
		{http.MethodGet, "/readyz", s.handleReady},

		{http.MethodPost, "/api/v1/scan", s.handleScan},

		{http.MethodGet, "/api/v1/whatsapp/webhook", s.handleWebhookVerify},
		{http.MethodPost, "/api/v1/whatsapp/webhook", s.handleWebhookReceive},

		{http.MethodGet, "/api/v1/receipts", s.handleListReceipts},
		{http.MethodPost, "/api/v1/receipts", s.handleCreateReceipt},
		{http.MethodGet, "/api/v1/receipts/{id}", s.handleGetReceipt},
	}
}

func (s *Server) Routes() http.Handler {
	table := s.routeTable()

	mux := http.NewServeMux()

	pathsOnly := http.NewServeMux()
	registered := make(map[string]bool)

	for _, rt := range table {
		mux.HandleFunc(rt.method+" "+rt.path, rt.handler)

		if !registered[rt.path] {
			registered[rt.path] = true

			pathsOnly.Handle(rt.path, http.NotFoundHandler())
		}
	}

	mux.Handle("/", s.handleUnmatched(table, pathsOnly))

	return httpx.Chain(mux,
		httpx.RequestID,
		httpx.Logger(s.logger),
		httpx.Recoverer(s.logger),
	)
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
