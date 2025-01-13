package main

import (
	"flag"
	"time"
)

type PerformanceConfig struct {
	MaxConcurrentRequests int
	RequestTimeout        time.Duration
	MaxConcurrentFFmpeg   int
}

var gPerformanceConfig PerformanceConfig

func parseFlag() {

	// Define flags
	maxConcurrentRequests := flag.Int(
		"max-concurrent-requests", PERF_HTTP_MAX_CONCURRENT, "Maximum number of concurrent requests allowed")
	requestTimeout := flag.Duration(
		"request-timeout", PERF_HTTP_TIMEOUT, "Timeout duration for requests (e.g., 30s, 1m)")
	maxConcurrentFFmpeg := flag.Int(
		"max-concurrent-ffmpeg", PERF_FFMPEG_MAX_CONCURRENT, "Maximum number of concurrent ffmpeg processes allowed")
	debug := flag.Bool("debug", false, "Enable debug mode")
	password := flag.String("password", "", "Session password; when empty randomly generated")

	// Parse flags
	flag.Parse()

	// Create and return the configuration
	gPerformanceConfig = PerformanceConfig{
		MaxConcurrentRequests: *maxConcurrentRequests,
		RequestTimeout:        *requestTimeout,
		MaxConcurrentFFmpeg:   *maxConcurrentFFmpeg,
	}

	//
	gAppInfo.Debug = *debug
	logDebug("Debug is enabled")

	//
	if *password == "" {
		*password, _ = generateRandomString(2)
		*password = "random"+*password
	}
	gAuthInfo.SessionPassword = *password
	logInfo("Password is", *password, "and", BAD_TRIES_TOLERANCE, "consecutive bad tries force shutdowns the server")

}
