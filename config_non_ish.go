// +build !linux !386
// !linux or !386


package main

import (
	"time"
)

const PERF_HTTP_MAX_CONCURRENT = 20000
const PERF_HTTP_TIMEOUT = time.Second * 30
const PERF_FFMPEG_MAX_CONCURRENT = 30

const IO_EACH_CACHE_COOLDOWN = 0