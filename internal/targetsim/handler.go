// Package targetsim implements the deterministic target-service simulator
// required by FAILURE_MODES.md: a reproducible stand-in for a real webhook
// receiver, so reliability tests don't depend on a third-party service.
//
// Routes:
//
//	GET/POST /sim/status/{code}                     - always returns {code}
//	GET/POST /sim/latency/{ms}/{code}                - sleeps {ms}ms, then returns {code}
//	GET/POST /sim/hang                               - never responds (until the client gives up)
//	GET/POST /sim/reset                              - closes the connection without a response
//	GET/POST /sim/flaky/{name}?fail=N&failStatus=500&okStatus=200
//	                                                  - returns failStatus for the first N requests
//	                                                    sharing {name}, okStatus after
//	GET/POST /sim/idempotent/{name}                  - 201 the first time a given Idempotency-Key is
//	                                                    seen for {name}, 200 on repeats
package targetsim

import (
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// NewHandler builds the simulator's complete HTTP handler.
func NewHandler() http.Handler {
	s := &simulator{flaky: make(map[string]int), seenKeys: make(map[string]bool)}

	mux := http.NewServeMux()
	mux.HandleFunc("/sim/status/{code}", s.handleStatus)
	mux.HandleFunc("/sim/latency/{ms}/{code}", s.handleLatency)
	mux.HandleFunc("/sim/hang", s.handleHang)
	mux.HandleFunc("/sim/reset", s.handleReset)
	mux.HandleFunc("/sim/flaky/{name}", s.handleFlaky)
	mux.HandleFunc("/sim/idempotent/{name}", s.handleIdempotent)
	return mux
}

type simulator struct {
	mu       sync.Mutex
	flaky    map[string]int  // name -> requests served so far
	seenKeys map[string]bool // name+"\x00"+idempotency-key -> seen
}

func (s *simulator) handleStatus(w http.ResponseWriter, r *http.Request) {
	code, err := strconv.Atoi(r.PathValue("code"))
	if err != nil {
		http.Error(w, "invalid status code", http.StatusBadRequest)
		return
	}
	w.WriteHeader(code)
}

func (s *simulator) handleLatency(w http.ResponseWriter, r *http.Request) {
	ms, err := strconv.Atoi(r.PathValue("ms"))
	if err != nil {
		http.Error(w, "invalid latency", http.StatusBadRequest)
		return
	}
	code, err := strconv.Atoi(r.PathValue("code"))
	if err != nil {
		http.Error(w, "invalid status code", http.StatusBadRequest)
		return
	}

	select {
	case <-time.After(time.Duration(ms) * time.Millisecond):
		w.WriteHeader(code)
	case <-r.Context().Done():
	}
}

// handleHang never writes a response, simulating a target that never
// replies; it only returns when the client gives up (request canceled).
func (s *simulator) handleHang(w http.ResponseWriter, r *http.Request) {
	// Consume POST bytes so net/http can observe the peer closing the connection.
	if _, err := io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, 1<<20)); err != nil {
		return
	}
	<-r.Context().Done()
}

// handleReset simulates the target abruptly closing the connection
// (TCP RST), rather than sending any HTTP response.
func (s *simulator) handleReset(w http.ResponseWriter, r *http.Request) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0) // send RST instead of a clean FIN on close
	}
	_ = conn.Close()
}

func (s *simulator) handleFlaky(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	failUntil, _ := strconv.Atoi(r.URL.Query().Get("fail"))
	failStatus, err := strconv.Atoi(r.URL.Query().Get("failStatus"))
	if err != nil {
		failStatus = http.StatusInternalServerError
	}
	okStatus, err := strconv.Atoi(r.URL.Query().Get("okStatus"))
	if err != nil {
		okStatus = http.StatusOK
	}

	s.mu.Lock()
	s.flaky[name]++
	seen := s.flaky[name]
	s.mu.Unlock()

	if seen <= failUntil {
		w.WriteHeader(failStatus)
		return
	}
	w.WriteHeader(okStatus)
}

func (s *simulator) handleIdempotent(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	key := r.Header.Get("Idempotency-Key")

	s.mu.Lock()
	compositeKey := name + "\x00" + key
	alreadySeen := s.seenKeys[compositeKey]
	s.seenKeys[compositeKey] = true
	s.mu.Unlock()

	if alreadySeen {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusCreated)
}
