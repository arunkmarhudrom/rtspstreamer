package main

import (
	"flag"
	"fmt"
	"net/http"
	"time"

	"rtspstreamer/internal/api"
	"rtspstreamer/internal/config"
	"rtspstreamer/internal/logging"
	"rtspstreamer/internal/onvifx"
	"rtspstreamer/internal/streaming"
)

func main() {
	var (
		cameraFile = flag.String("cameras", "camerainfo.json", "path to camera config")
		listenAddr = flag.String("listen", ":8080", "http listen address")
	)
	flag.Parse()

	logger, err := logging.New("logs")
	if err != nil {
		panic(err)
	}

	cameras, err := config.LoadCameras(*cameraFile)
	if err != nil {
		logger.Fatalf("load cameras: %v", err)
	}
	logger.Printf("loaded cameras=%d from=%s", len(cameras), *cameraFile)

	resolver := onvifx.New(logger)
	manager := streaming.NewManager(logger, cameras, resolver)
	httpServer := api.NewServer(logger, manager)

	srv := &http.Server{
		Addr:              *listenAddr,
		Handler:           httpServer.Router(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	logger.Printf("service ready at http://localhost%s", *listenAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatalf("http server: %v", err)
	}
	fmt.Println("exit")
}
