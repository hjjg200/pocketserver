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
	mu sync.Mutex

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
	mu				sync.RWMutex
	detailsWg		sync.WaitGroup
	changed			bool
	changedDetails	bool
	json			[]byte
	dir				string
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

func (mgr *MetadataManager) Get(dir string, waitDetails bool) (data []byte, ok bool) {

	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	cache, ok := mgr.cacheMap[dir]
	if !ok {
		return
	}

	data = cache.get(waitDetails)
	ok = true
	return

}

func (cache *metadataCache) get(waitDetails bool) ([]byte) {
	
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	
	if waitDetails {
		cache.detailsWg.Wait()

		if cache.changedDetails {
			logDebug("details changed")
			cache.changedDetails = false
			cache.changed = true
		}
	}

	if cache.changed == true {
		logDebug("cache changed")
		cache.changed = false

		data, err := json.Marshal(cache.body)
		if err != nil {
			panic(err)
		}
		cache.json = data
	}

	return cache.json

}

func (mgr *MetadataManager) AddDir(dir string) {

	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	if _, ok := mgr.cacheMap[dir]; ok {
		logFatal(fmt.Errorf("Directory already exists in cache: %s", dir))
	}

	mgr.cacheMap[dir] = &metadataCache{
		mgr: mgr,
		dir: dir,
		body: make(MetadataMap),
	}

}

func (cache *metadataCache) update() error {

	dir := cache.dir

	var added, removed int

	// Check if changed
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

			// Check if it is eligible for details
			if meta.MimeCategory == MIME_AUDIO ||
				meta.MimeCategory == MIME_VIDEO ||
				ext == ".webp" {
				
				cache.populateMetadataDetails(fullpath, meta)

			}

			mm1[base] = meta

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
		cache.changed = true
	}

	return nil

}

func (mgr *MetadataManager) UpdateDir(dir string) error {

	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	cache, ok := mgr.cacheMap[dir]
	if !ok {
		return fmt.Errorf("Not found")
	}

	return cache.update()

}


func (cache *metadataCache) populateMetadataDetails(fullpath string, meta *Metadata) {
	if cache.checkMetadataDetails(fullpath, meta) == false {
		cache.scheduleMetadataDetails(fullpath, meta)
	}
}

func (cache *metadataCache) checkMetadataDetails(fullpath string, meta *Metadata) bool {

	metapath  := filepath.Join(gAppInfo.MetadataDir, fullpath)
	txtpath   := metapath + META_EXT_TXT
	thumbpath := metapath + META_EXT_THUMB
	smallpath := metapath + META_EXT_THUMB_SMALL

	txt, err := os.ReadFile(txtpath)
	if err != nil {
		logDebug(txtpath, err)
		return false
	}

	err = json.Unmarshal(txt, &meta.Details)
	if err != nil {
		logDebug(txtpath, err)
		return false
	}

	// If malformed metadata
	if meta.Details["duration"] == "" {
		return false
	}

	// Thumbnail
	_, err = os.Stat(thumbpath)
	if err != nil {
		logDebug(txtpath, err)
		return false
	}

	// Thumbnail for audio
	if meta.MimeCategory == "audio" {
		_, err = os.Stat(smallpath)
		if err != nil {
			logDebug(smallpath, err)
			return false
		}
	}

	return true

}

func (cache *metadataCache) scheduleMetadataDetails(fullpath string, meta *Metadata) {

	cache.detailsWg.Add(1)

	go func() {

		defer cache.detailsWg.Done()

		cache.mgr.bakeSem.Acquire()
		defer cache.mgr.bakeSem.Release()

		// Check if other goroutine did the task
		meta.mu.Lock()
		defer meta.mu.Unlock()
		if cache.checkMetadataDetails(fullpath, meta) {
			logDebug(fullpath, "another goroutine baked metadata")
			return
		}

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
