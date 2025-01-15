// +build linux,386
// linux and 386
// build for iSH

package main

import (
	"time"
)

const PERF_HTTP_MAX_CONCURRENT = 15
const PERF_HTTP_TIMEOUT = time.Second * 30
const PERF_FFMPEG_MAX_CONCURRENT = 1

const IO_EACH_CACHE_COOLDOWN = time.Second * 5