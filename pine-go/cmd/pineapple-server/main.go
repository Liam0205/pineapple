package main

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"runtime/debug"

	pine "github.com/Liam0205/pineapple/pine-go"
	// Blank-import all operator packages to trigger init() registrations.
	_ "github.com/Liam0205/pineapple/pine-go/operators"
	"github.com/Liam0205/pineapple/pine-go/pkg/server"
)

// demoRoutes returns the demo custom route used by cross-validate to verify
// Route/Ingress/Egress behavior across the three engines. POST /api/echo
// accepts the same request body as /execute and responds with the pipeline's
// common output only.
func demoRoutes() []server.Route {
	return []server.Route{{
		Method: http.MethodPost,
		Path:   "/api/echo",
		Ingress: func(r *http.Request) (*pine.Request, error) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				// Propagate read errors as-is so the server's central 413
				// handling sees http.MaxBytesError when the body cap trips.
				return nil, err
			}
			var req struct {
				Common map[string]any   `json:"common"`
				Items  []map[string]any `json:"items"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, errors.New("invalid request body")
			}
			return &pine.Request{Common: req.Common, Items: req.Items}, nil
		},
		Egress: func(w http.ResponseWriter, r *http.Request, result *pine.Result, err error) {
			w.Header().Set("Content-Type", "application/json")
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
				return
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"common": result.Common})
		},
	}}
}

func main() {
	// Reduce GC frequency for throughput-oriented workloads.
	// Only apply if the user hasn't explicitly set GOGC.
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(400)
	}

	configPath := flag.String("config", "", "Path to pipeline JSON config file")
	addr := flag.String("addr", ":8080", "Listen address")
	adminAddr := flag.String("admin-addr", "", "Admin listen address for pprof (e.g. :6060); empty disables")
	readHeaderTimeout := flag.Duration("read-header-timeout", 0, "HTTP read header timeout (0 = default 10s)")
	readTimeout := flag.Duration("read-timeout", 0, "HTTP read timeout (0 = default 30s)")
	writeTimeout := flag.Duration("write-timeout", 0, "HTTP write timeout (0 = default 60s)")
	idleTimeout := flag.Duration("idle-timeout", 0, "HTTP idle timeout (0 = default 120s)")
	maxBodySize := flag.Int64("max-body-size", 0, "Max request body size in bytes (0 = default 10MB)")
	watch := flag.Bool("watch", true, "Enable config hot-reload watcher")
	demo := flag.Bool("demo-routes", false, "Register the demo custom route POST /api/echo (for cross-validation)")
	flag.Parse()

	var routes []server.Route
	if *demo {
		routes = demoRoutes()
	}

	if err := server.Run(server.Config{
		ConfigPath:         *configPath,
		Addr:               *addr,
		AdminAddr:          *adminAddr,
		ReadHeaderTimeout:  *readHeaderTimeout,
		ReadTimeout:        *readTimeout,
		WriteTimeout:       *writeTimeout,
		IdleTimeout:        *idleTimeout,
		MaxRequestBodySize: *maxBodySize,
		Routes:             routes,
		Watch:              pine.Bool(*watch),
	}); err != nil {
		log.Fatal(err)
	}
}
