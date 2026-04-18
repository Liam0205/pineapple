package main

import (
	"flag"
	"log"

	// Blank-import all operator packages to trigger init() registrations.
	_ "github.com/Liam0205/pineapple/operators"
	"github.com/Liam0205/pineapple/pkg/server"
)

func main() {
	configPath := flag.String("config", "", "Path to pipeline JSON config file")
	addr := flag.String("addr", ":8080", "Listen address")
	flag.Parse()

	if err := server.Run(server.Config{
		ConfigPath: *configPath,
		Addr:       *addr,
	}); err != nil {
		log.Fatal(err)
	}
}
