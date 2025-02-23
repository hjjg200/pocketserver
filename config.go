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
	debug2 := flag.String("d", "_DISABLED_", "Enable debug channels, * for all debug channels")
	password := flag.String("password", "", "Session password; when empty randomly generated")
	test := flag.String("T", "", "Test options")
	testVar := flag.String("Tv", "", "Test var")

	// Parse flags
	flag.Parse()

	// Create and return the configuration
	gPerformanceConfig = PerformanceConfig{
		MaxConcurrentRequests: *maxConcurrentRequests,
		RequestTimeout:        *requestTimeout,
		MaxConcurrentFFmpeg:   *maxConcurrentFFmpeg,
	}

	//
	gAppInfo.Test = *test
	gAppInfo.TestVar = *testVar
	gAppInfo.Debug = *debug || *test != "" || *debug2 != "_DISABLED_"
	gAppInfo.Debug2 = *debug2
	logDebug("Debug is enabled")

	//
	if *password == "" {
		*password, _ = generateRandomString(2)
		*password = "random"+*password
	}
	gAuthInfo.SessionPassword = *password
	logInfo("Password is", *password, "and", BAD_TRIES_TOLERANCE, "consecutive bad tries force shutdowns the server")

}
