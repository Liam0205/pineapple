package main

import (
	"flag"
	"log"
	"os"
	"runtime/debug"

	// Blank-import all operator packages to trigger init() registrations.
	_ "github.com/Liam0205/pineapple/pine-go/operators"
	"github.com/Liam0205/pineapple/pine-go/pkg/server"
)

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
	flag.Parse()

	if err := server.Run(server.Config{
		ConfigPath:         *configPath,
		Addr:               *addr,
		AdminAddr:          *adminAddr,
		ReadHeaderTimeout:  *readHeaderTimeout,
		ReadTimeout:        *readTimeout,
		WriteTimeout:       *writeTimeout,
		IdleTimeout:        *idleTimeout,
		MaxRequestBodySize: *maxBodySize,
	}); err != nil {
		log.Fatal(err)
	}
}
