// Multi-pipeline server example — one process, several pipelines, each bound
// to its own endpoint with its own log prefix.
//
// Layout:
//
//	POST /api/feed    -> feed.json    (log_prefix "[feed] ")
//	POST /api/search  -> search.json  (log_prefix "[search] ")
//	POST /execute     -> 410 Gone    (legacy endpoint, deliberately retired)
//
// Instead of the bundled single-pipeline server (server.Run, which owns
// /execute), each pipeline is an embedded runtime built with server.NewServer
// (issue #169): engine + resources + hot-reload watcher, no HTTP. The HTTP
// layer below is a plain net/http mux the application owns, so pipelines map
// to endpoints however you like — the same pattern works under Gin/Echo by
// calling Server.Execute from your framework handlers.
//
// Since issue #172 log_prefix is engine-scoped: lines emitted while the feed
// pipeline executes (observe_log, [pine-debug], operator DebugLog) carry
// "[feed] " while search lines carry "[search] ", concurrently, in one
// process. Nothing touches the global log package.
//
// The built-in /execute path is kept only as a tombstone: it answers
// 410 Gone and points callers at the named endpoints.
//
// Run:
//
//	cd pine-go/examples/multi-pipeline
//	go run . -feed feed.json -search search.json -addr :8080
//
// Try:
//
//	curl -s -X POST localhost:8080/api/feed   -d '{"common":{"user_id":"u1"}}'
//	curl -s -X POST localhost:8080/api/search -d '{"common":{"query":"tech"}}'
//	curl -si -X POST localhost:8080/execute   -d '{}'   # 410 Gone
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"net/http"

	pine "github.com/Liam0205/pineapple/pine-go"
	// Blank-import all operator packages to trigger init() registrations.
	_ "github.com/Liam0205/pineapple/pine-go/operators"
	"github.com/Liam0205/pineapple/pine-go/pkg/server"
)

func main() {
	feedCfg := flag.String("feed", "feed.json", "feed pipeline config (declares log_prefix \"[feed] \")")
	searchCfg := flag.String("search", "search.json", "search pipeline config (declares log_prefix \"[search] \")")
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	// One embedded runtime per pipeline. Each config declares its own
	// log_prefix; since issue #172 the prefix scopes to its engine, so the
	// two runtimes below log under different prefixes in the same process.
	feed, err := server.NewServer(server.Config{ConfigPath: *feedCfg})
	if err != nil {
		log.Fatalf("feed pipeline: %v", err)
	}
	defer feed.Close()

	search, err := server.NewServer(server.Config{ConfigPath: *searchCfg})
	if err != nil {
		log.Fatalf("search pipeline: %v", err)
	}
	defer search.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/feed", pipelineHandler(feed))
	mux.HandleFunc("/api/search", pipelineHandler(search))
	// Legacy endpoint: kept so old callers get a clear migration signal
	// instead of a generic 404, but it no longer runs any pipeline.
	mux.HandleFunc("/execute", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusGone, map[string]string{
			"error": "endpoint retired: use /api/feed or /api/search",
		})
	})

	log.Printf("listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

// maxBodyBytes mirrors the bundled server's default request-body cap. An
// embedding HTTP layer must keep this boundary itself — issue #169 treats
// the body limit as a shared-dispatch-layer safety contract, and dropping
// it here would let one oversized request grow process memory unbounded.
const maxBodyBytes = 10 << 20 // 10 MB

// pipelineHandler adapts HTTP to one embedded pipeline runtime. Execute
// acquires the live snapshot with an in-flight reference, so a concurrent
// hot-reload never tears the engine down mid-request.
func pipelineHandler(rt *server.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
				return
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		var req struct {
			Common map[string]any   `json:"common"`
			Items  []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if req.Common == nil {
			req.Common = map[string]any{}
		}

		result, err := rt.Execute(r.Context(), &pine.Request{Common: req.Common, Items: req.Items})
		if err != nil {
			status := http.StatusInternalServerError
			var ve *pine.ValidationError
			if errors.As(err, &ve) {
				status = http.StatusBadRequest
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"common": result.Common,
			"items":  result.Items,
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
