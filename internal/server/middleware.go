package server

import (
	"net/http"
	"sync/atomic"
)

// Middleware provides HTTP middleware for CORS handling and concurrency limiting.
type Middleware struct {
	sem            chan struct{}
	activeRequests int64
}

// NewMiddleware constructs a Middleware instance with a semaphore channel of capacity maxConcurrency.
func NewMiddleware(maxConcurrency int) *Middleware {
	if maxConcurrency <= 0 {
		maxConcurrency = 4
	}
	return &Middleware{
		sem: make(chan struct{}, maxConcurrency),
	}
}

// ActiveRequests returns the current number of in-flight requests running under LimitConcurrency.
func (m *Middleware) ActiveRequests() int64 {
	return atomic.LoadInt64(&m.activeRequests)
}

// CORS wraps an http.Handler to attach standard cross-origin resource sharing headers
// and respond to OPTIONS preflight requests with HTTP 200 OK.
func (m *Middleware) CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// LimitConcurrency throttles in-flight requests using a channel-based semaphore
// and atomically tracks active request counts for observability.
func (m *Middleware) LimitConcurrency(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case m.sem <- struct{}{}:
		}
		atomic.AddInt64(&m.activeRequests, 1)
		defer func() {
			<-m.sem
			atomic.AddInt64(&m.activeRequests, -1)
		}()

		next.ServeHTTP(w, r)
	})
}
