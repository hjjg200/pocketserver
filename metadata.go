package main

import (
	"time"
	"fmt"
	"sync"
	"path/filepath"
	"os"
	"strings"
	"encoding/json"
	"mime"
)

type Metadata struct {

	ModTime  time.Time `json:"modTime"`
	Size     int64     `json:"size"`
	IsDir    bool      `json:"isDir"`

	MimeCategory string `json:"mimeCategory"`
	
	Details   map[string] string `json:"details"`

	//Title    string `json:"title"`
	//Album    string `json:"album"`
	//Artist   string `json:"artist"`
	//Duration string `json:"duration"`
}
type MetadataMap map[string]*Metadata

type metadataCache struct {
	mgr			*MetadataManager

	body			MetadataMap
	detailsWg		sync.WaitGroup
	changed			bool
	changedDetails	bool
	json			[]byte
	dir				string
	mod				time.Time
}

type MetadataManager struct {
	bakeSem		*Semaphore
	cacheMap	map[string]*metadataCache
	mu			sync.RWMutex
}


func NewMetadataManager() *MetadataManager {

	mgr := &MetadataManager{}

	mgr.bakeSem		= NewSemaphore(gPerformanceConfig.MaxConcurrentFFmpeg, 0)
	mgr.cacheMap	= make(map[string]*metadataCache)

	return mgr

}

func (mgr *MetadataManager) Get(dir string, waitDetails bool) (data []byte, mod time.Time, ok bool) {

	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	cache, ok := mgr.cacheMap[dir]
	if !ok {
		return
	}

	data, mod = cache.get(waitDetails)
	ok = true
	return

}

func (cache *metadataCache) updateJson() {

	data, err := json.Marshal(cache.body)
	if err != nil {
		panic(err)
	}
	cache.json = data

	err = os.WriteFile(cache.mgr.formatDirCacheName(cache.dir), data, 0644)
	if err != nil {
		logError("Failed to write cache file", cache.dir)
	}

}

func (cache *metadataCache) get(waitDetails bool) ([]byte, time.Time) {
	
	if waitDetails {
		cache.detailsWg.Wait() // TODO fix race

		if cache.changedDetails {
			logDebug("details changed")
			cache.changedDetails = false
			
			cache.updateJson()
			cache.mod = time.Now()
		}
	}

	return cache.json, cache.mod

}

func (mgr *MetadataManager) formatDirCacheName(dir string) string {
	dir = strings.ReplaceAll(dir, "/", "")
	dir = strings.ReplaceAll(dir, "\\", "")
	return filepath.Join(gAppInfo.MetadataDir, dir) + ".json"
}

func (mgr *MetadataManager) AddDir(dir string) {

	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	if _, ok := mgr.cacheMap[dir]; ok {
		logFatal(fmt.Errorf("Directory already exists in cache: %s", dir))
	}

	cache := &metadataCache{
		mgr: mgr,
		dir: dir,
		body: make(MetadataMap),
	}
	mgr.cacheMap[dir] = cache

	data, err := os.ReadFile(mgr.formatDirCacheName(dir))
	if err != nil {
		return
	}

	cache.json = data
	err = json.Unmarshal(data, &cache.body)
	if err != nil {
		logFatal("Failed to read cached data for", dir)
	}

}

func (cache *metadataCache) update() error {

	// Cancel too frequent update
	if time.Since(cache.mod) < IO_EACH_CACHE_COOLDOWN {
		logDebug("too soon to update cache")
		return nil
	}

	dir := cache.dir

	var added, removed int

	// Check if changed
	logInfo("Caching for", dir, "starting")
	dentries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("Cannot read directory %s: %w", dir, err)
	}

	// Create a copy of the current map
	mm0 := cache.body
	mm1 := make(MetadataMap, len(mm0))

	// Detect additions and build the new map
	for _, dentry := range dentries {

		fullpath := filepath.Join(dir, dentry.Name())
		base	 := dentry.Name()
		ext 	 := filepath.Ext(base)

		if _, ok := mm0[base]; ok {

			mm1[base] = mm0[base]

		} else {

			added++

			// TODO: Cache and metadata updates for new file
			info, err := dentry.Info()
			if err != nil {
				logWarn("Failed to read info of", fullpath, err)
				continue
			}
			
			meta 			 := &Metadata{}
			meta.ModTime 	  = info.ModTime()
			meta.Size		  = info.Size()
			meta.IsDir		  = info.IsDir()
			meta.MimeCategory = getMimeCategory(fullpath)

			mm1[base] = meta

		}

		// Check if it is eligible for details
		if mm1[base].MimeCategory == MIME_AUDIO ||
			mm1[base].MimeCategory == MIME_VIDEO ||
			ext == ".webp" {
			
			cache.ensureMetadataDetails(fullpath, mm1[base])

		}

	}

	// Detect removals
	for base := range mm0 {
		if _, ok := mm1[base]; !ok {
			removed++
			// TODO: Handle removed file (e.g., clean up metadata)
		}
	}

	// Swap the maps atomically
	cache.body = mm1

	logInfo("Updated cache of", dir, "-", added, "added", removed, "removed")

	if added != 0 || removed != 0 {
		cache.updateJson()
		cache.mod = time.Now()
	}

	return nil

}

func (mgr *MetadataManager) UpdateDir(dir string) error {

	mgr.mu.Lock()

	cache, ok := mgr.cacheMap[dir]
	if !ok {
		mgr.mu.Unlock()

		return fmt.Errorf("Not found")
	}

	 go func() {
		err := cache.update()
		if err != nil {
			logError("Background cache update failed", dir, "err:", err)
		}

		mgr.mu.Unlock()
	 }()

	 return nil

}

func (cache *metadataCache) ensureMetadataDetails(fullpath string, meta *Metadata) {

	// Early exit
	if meta.Details["duration"] == "" {
		cache.scheduleMetadataDetails(fullpath, meta)
		return
	}

	metapath  := filepath.Join(gAppInfo.MetadataDir, fullpath)
	txtpath   := metapath + META_EXT_TXT
	thumbpath := metapath + META_EXT_THUMB
	smallpath := metapath + META_EXT_THUMB_SMALL

	txt, err := os.ReadFile(txtpath)
	if err != nil {
		logDebug(txtpath, err)
		cache.scheduleMetadataDetails(fullpath, meta)
		return
	}

	err = json.Unmarshal(txt, &meta.Details)
	if err != nil {
		logDebug(txtpath, err)
		cache.scheduleMetadataDetails(fullpath, meta)
		return
	}

	// If malformed metadata
	if meta.Details["duration"] == "" {
		cache.scheduleMetadataDetails(fullpath, meta)
		return
	}

	// Thumbnail
	_, err = os.Stat(thumbpath)
	if err != nil {
		logDebug(txtpath, err)
		cache.scheduleMetadataDetails(fullpath, meta)
		return
	}

	// Thumbnail for audio
	if meta.MimeCategory == "audio" {
		_, err = os.Stat(smallpath)
		if err != nil {
			logDebug(smallpath, err)
			cache.scheduleMetadataDetails(fullpath, meta)
			return
		}
	}

}

func (cache *metadataCache) scheduleMetadataDetails(fullpath string, meta *Metadata) {

	cache.detailsWg.Add(1)

	go func() {

		defer cache.detailsWg.Done()

		cache.mgr.bakeSem.Acquire()
		defer cache.mgr.bakeSem.Release()

		err := cache.mgr.bakeMetadataDetails(fullpath, meta)
		if err != nil {
			logWarn("Failed to bake metadata details", fullpath, err)
		}

		cache.changedDetails = true

	}()

}

func getMimeCategory(path string) string {

	// Treat the extensions handled specially only
	// image/* audio/* video/*

	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return ""
	}

	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		return ""
	}

	return strings.Split(mimeType, "/")[0]

/*
	cat := ""

	videos := []string{
		".mp4", ".webm", ".mov", ".mkv",
	}
	images := []string{
		".webp", ".jpg", ".jpeg", ".gif", ".heic", ".heif", ".tiff", ".tif", ".png",
	}
	audios := []string{
		".mp3", ".opus", // TODO iOS opus handling
	}

	if slices.Contains(videos, ext) {
		cat = MIME_VIDEO
	} else if slices.Contains(images, ext) {
		cat = MIME_IMAGE
	} else if slices.Contains(audios, ext) {
		cat = MIME_AUDIO
	}

	return cat
*/

}


func (mgr *MetadataManager) bakeMetadataDetails(fullpath string, meta *Metadata) (err error) {

	cat		  := meta.MimeCategory
	metapath  := filepath.Join(gAppInfo.MetadataDir, fullpath)
	smallpath := metapath + META_EXT_THUMB_SMALL
	thumbpath := metapath + META_EXT_THUMB
	txtpath	  := metapath + META_EXT_TXT

	// Make directory
	err = os.MkdirAll(filepath.Dir(metapath), 0755)
	if err != nil {
		return fmt.Errorf("failed to make directory %s: %w", fullpath, err)
	}

	parseMetadataLine := func(line string, key string, endAt string) (bool, string) {

		if strings.Contains(line, key) == false {
			return false, ""
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return false, ""
		}

		if key != strings.TrimSpace(parts[0]) {
			return false, ""
		}

		val := strings.TrimSpace(parts[1])
		
		if endAt != "" {
			val = strings.SplitN(val, endAt, 2)[0]
		}

		return true, val

	}

	// Artwork
	cmd := FFMPEG_CMD_BASE
	if cat == "audio" {
		cmd = fmt.Sprintf(cmd+FFMPEG_CMD_AUDIO_THUMB, fullpath, thumbpath)
	} else {
		cmd = fmt.Sprintf(cmd+FFMPEG_CMD_VIDEO_THUMB, fullpath, thumbpath)
	}

	out, err := executeFFmpeg(cmd)
	if err != nil {
		return fmt.Errorf("Failed to execute ffmpeg %s: %w", fullpath, err)
	}

	// Parse metadata lines
	meta.Details = make(map[string]string)
	for _, line := range strings.Split(out, "\n") {

		if cat == "audio" {
			ok, title := parseMetadataLine(line, "title", "")
			if ok {
				meta.Details["title"] = title
			}
			ok, artist := parseMetadataLine(line, "artist", "")
			if ok {
				meta.Details["artist"] = artist
			}
			ok, album := parseMetadataLine(line, "album", "")
			if ok {
				meta.Details["album"] = album
			}
		}

		ok, duration := parseMetadataLine(line, "Duration", ",")
		if ok {
			meta.Details["duration"] = duration
		}

	}

	// Write json
	txt, err := json.MarshalIndent(meta.Details, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON %s: %w", fullpath, err)
	}
	err = os.WriteFile(txtpath, txt, 0644)
	if err != nil {
		return fmt.Errorf("Failed to write thumbnail %s: %w", fullpath, err)
	}

	// Check if malformed media
	if _, ok := meta.Details["duration"]; !ok {
		return fmt.Errorf("Malformed media file", fullpath)
	}

	// Check for image/webp
	if meta.Details["duration"] == "N/A" ||
		meta.Details["duration"][:8] == "00:00:00" {
		err = os.WriteFile(thumbpath, []byte{}, 0644)
		if err != nil {
			return fmt.Errorf("Failed to write thumbnail %s: %w", fullpath, err)
		}
	}

	// Thumbnail not created
	_, err = os.Stat(thumbpath)
	if err != nil {
		err = os.WriteFile(thumbpath, []byte{}, 0644)
		if err != nil {
			return fmt.Errorf("Failed to write thumbnail %s: %w", fullpath, err)
		}
	}

	// Small thumbnail for audio
	if cat == "audio" {

		smallcmd := fmt.Sprintf(
			FFMPEG_CMD_BASE+FFMPEG_CMD_AUDIO_THUMB_SMALL, 
			fullpath, smallpath)
		_, err = executeFFmpeg(smallcmd)
		if err != nil {
			return fmt.Errorf("Failed to execute ffmpeg %s: %w", fullpath, err)
		}

		_, err = os.Stat(smallpath)
		if err != nil {
			err = os.WriteFile(smallpath, []byte{}, 0644)
			if err != nil {
				return fmt.Errorf("Failed to write small thumbnail %s: %w", fullpath, err)
			}
		}

	}

	return nil

}
