package main

import (
	"flag"
	"log"

	// Blank-import all operator packages to trigger init() registrations.
	_ "github.com/Liam0205/pineapple/pine-go/operators"
	"github.com/Liam0205/pineapple/pine-go/pkg/server"
)

func main() {
	configPath := flag.String("config", "", "Path to pipeline JSON config file")
	addr := flag.String("addr", ":8080", "Listen address")
	readHeaderTimeout := flag.Duration("read-header-timeout", 0, "HTTP read header timeout (0 = default 10s)")
	readTimeout := flag.Duration("read-timeout", 0, "HTTP read timeout (0 = default 30s)")
	writeTimeout := flag.Duration("write-timeout", 0, "HTTP write timeout (0 = default 60s)")
	idleTimeout := flag.Duration("idle-timeout", 0, "HTTP idle timeout (0 = default 120s)")
	maxBodySize := flag.Int64("max-body-size", 0, "Max request body size in bytes (0 = default 10MB)")
	flag.Parse()

	if err := server.Run(server.Config{
		ConfigPath:         *configPath,
		Addr:               *addr,
		ReadHeaderTimeout:  *readHeaderTimeout,
		ReadTimeout:        *readTimeout,
		WriteTimeout:       *writeTimeout,
		IdleTimeout:        *idleTimeout,
		MaxRequestBodySize: *maxBodySize,
	}); err != nil {
		log.Fatal(err)
	}
}
