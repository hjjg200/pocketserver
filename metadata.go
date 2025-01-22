package main

import (
	"time"
	"fmt"
	"sync"
	"sync/atomic"
	"path/filepath"
	"os"
	"strings"
	"encoding/json"
	"mime"
)

type Metadata struct {
	ModTime			time.Time `json:"modTime"`
	Size			int64     `json:"size"`
	IsDir			bool      `json:"isDir"`
	MimeCategory	string `json:"mimeCategory"`
	
	Details			map[string] string `json:"details"`
}
type MetadataMap map[string]*Metadata
type MetadataBody struct {
	MetaMap		MetadataMap `json:"metaMap"`
	Playlist	[]string `json:"playlist"`
}

type metadataCache struct {
	mgr				*MetadataManager

	body			MetadataBody
	bodyMu			sync.Mutex
	detailsWg		sync.WaitGroup
	json			atomic.Pointer[[]byte]
	dir				string

	update			func()
}

type MetadataManager struct {
	bakeSem		*Semaphore
	cacheMap	map[string]*metadataCache
	cacheMapMu	sync.RWMutex // cache registration
	updateMu	sync.Mutex // only one update at a time
}


func NewMetadataManager() *MetadataManager {

	mgr := &MetadataManager{}

	mgr.bakeSem		= NewSemaphore(gPerformanceConfig.MaxConcurrentFFmpeg, 0)
	mgr.cacheMap	= make(map[string]*metadataCache)

	return mgr

}

func (mgr *MetadataManager) Get(dir string) ([]byte, bool) {

	mgr.cacheMapMu.RLock()
	defer mgr.cacheMapMu.RUnlock()

	cache, ok := mgr.cacheMap[dir]
	if !ok {
		return nil, false
	}

	return *cache.json.Load(), true

}

func (cache *metadataCache) updateJson() {

	data, err := json.Marshal(cache.body)
	if err != nil {
		panic(err)
	}
	cache.json.Store(&data)

	err = os.WriteFile(cache.mgr.formatDirCacheName(cache.dir), data, 0644)
	if err != nil {
		logError("Failed to write cache file", cache.dir)
	}

}

func (mgr *MetadataManager) EditPlaylist(dir string, pl1 []string) error {
	
	mgr.cacheMapMu.RLock()
	defer mgr.cacheMapMu.RUnlock()

	cache, ok := mgr.cacheMap[dir]
	if !ok {
		return fmt.Errorf("Dir not found")
	}

	return cache.editPlaylist(pl1)

}

func (cache *metadataCache) editPlaylist(pl1 []string) error {

	cache.bodyMu.Lock()
	defer cache.bodyMu.Unlock()

	for _, base := range pl1 {
		if meta, ok := cache.body.MetaMap[base]; !ok {
			return fmt.Errorf("File doesn't exist", base)
		} else {
			if meta.MimeCategory != MIME_AUDIO {
				return fmt.Errorf("Not an audio file", base)
			}
		}
	}

	cache.body.Playlist = pl1
	cache.updateJson()

	return nil

}

func (mgr *MetadataManager) parseDirCacheName(formatted string) string {
	splits := strings.Split(formatted, META_SLASH_IN_FILENAME)
	dir := filepath.Join(splits...)
	logDebug(formatted, dir)
	return dir
}

func (mgr *MetadataManager) formatDirCacheName(dir string) string {
	dir = strings.ReplaceAll(dir, "/", META_SLASH_IN_FILENAME)
	dir = strings.ReplaceAll(dir, "\\", META_SLASH_IN_FILENAME)
	return filepath.Join(gAppInfo.MetadataDir, dir) + ".json"
}

func (mgr *MetadataManager) AddDir(dir string) {

	mgr.cacheMapMu.Lock()
	defer mgr.cacheMapMu.Unlock()

	if _, ok := mgr.cacheMap[dir]; ok {
		logFatal(fmt.Errorf("Directory already exists in cache: %s", dir))
	}

	// Create new cache

	body := MetadataBody{}
	body.MetaMap = make(MetadataMap)
	body.Playlist = make([]string, 0)
	cache := &metadataCache{
		mgr:	mgr,
		dir:	dir,
		body:	body,
	}

	cache.update = throttle(cache._update, IO_EACH_CACHE_COOLDOWN)
	mgr.cacheMap[dir] = cache

	data, err := os.ReadFile(mgr.formatDirCacheName(dir))
	if err != nil {
		return
	}

	cache.json.Store(&data)
	err = json.Unmarshal(data, &cache.body)
	if err != nil {
		logFatal("Failed to read cached data for", dir)
	}

}

func (cache *metadataCache) _update() {

	dir := cache.dir

	var added, modified, removed int

	// Check if changed
	logInfo("Caching for", dir, "starting")
	dentries, err := os.ReadDir(dir)
	if err != nil {
		logError(fmt.Errorf("Cannot read directory %s: %w", dir, err))
		return
	}

	// Lock after ReadDir
	cache.bodyMu.Lock()

	// Create a copy of the current map
	mm0 := cache.body.MetaMap
	mm1 := make(MetadataMap, len(mm0))

	// Detect additions and build the new map
	for _, dentry := range dentries {

		fullpath	:= filepath.Join(dir, dentry.Name())
		base		:= dentry.Name()
		ext			:= filepath.Ext(base)
		info, err	:= dentry.Info()
		if err != nil {
			logWarn("Failed to read info of", fullpath, err)
			continue
		}

		if _, ok := mm0[base]; ok {

			if info.ModTime().Equal(mm0[base].ModTime) == false {
				modified++
			}
			mm1[base] = mm0[base]

		} else {

			mm1[base] = &Metadata{}
			added++

		}

		mm1[base].ModTime		= info.ModTime()
		mm1[base].Size			= info.Size()
		mm1[base].IsDir			= info.IsDir()
		mm1[base].MimeCategory	= getMimeCategory(fullpath)

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

	// PLAYLIST Check playlist
	var count	= 0
	var pl1		= make([]string, len(cache.body.Playlist))
	var pl1keys = make(map[string]struct{})

	// PLAYLSIT Handle removed
	for i, base := range cache.body.Playlist {
		if _, ok := mm1[base]; ok {
			pl1[i] = base
			pl1keys[base] = struct{}{}
			count++
		} else {
			logDebug("REMOVE FROM PLAYLIST", dir, base)
		}
	}
	pl1 = pl1[:count]

	// PLAYLIST append added
	for base := range mm1 {
		if _, ok := pl1keys[base]; !ok && mm1[base].MimeCategory == MIME_AUDIO {
			pl1 = append(pl1, base)
			logDebug("ADD TO PLAYLIST", dir, base)
		}
	}

	// 
	cache.body.MetaMap = mm1
	cache.body.Playlist = pl1

	logInfo("Updated cache of", dir, "-", added, "added,", modified, "modified,", removed, "removed")

	cache.detailsWg.Wait()
	cache.updateJson()

	cache.bodyMu.Unlock()

}

func (mgr *MetadataManager) UpdateDir(dir string) error {

	mgr.cacheMapMu.RLock()

	cache, ok := mgr.cacheMap[dir]
	if !ok {
		mgr.cacheMapMu.RUnlock()
		return fmt.Errorf("Not found")
	}
	mgr.cacheMapMu.RUnlock()

	mgr.updateMu.Lock()
	cache.update()
	mgr.updateMu.Unlock()

	 return nil

}

func (cache *metadataCache) ensureMetadataDetails(fullpath string, meta *Metadata) {

	cache.detailsWg.Add(1)

	if cache.checkMetadataDetails(fullpath, meta) == false {
		cache.scheduleMetadataDetails(fullpath, meta)
		return
	}

	cache.detailsWg.Done()

}

func (cache *metadataCache) checkMetadataDetails(fullpath string, meta *Metadata) bool {

	metapath  := filepath.Join(gAppInfo.MetadataDir, fullpath)
	txtpath   := metapath + META_EXT_TXT
	thumbpath := metapath + META_EXT_THUMB
	smallpath := metapath + META_EXT_THUMB_SMALL

	if d, ok := meta.Details["duration"]; !ok || d == "" {
		return false
	}

	txt, err := os.ReadFile(txtpath)
	if err != nil {
		return false
	}

	err = json.Unmarshal(txt, &meta.Details)
	if err != nil {
		return false
	}

	// If malformed metadata
	if meta.Details["duration"] == "" {
		return false
	}

	// Thumbnail
	_, err = os.Stat(thumbpath)
	if err != nil {
		return false
	}

	// Thumbnail for audio
	if meta.MimeCategory == "audio" {
		_, err = os.Stat(smallpath)
		if err != nil {
			return false
		}
	}

	return true

}

func (cache *metadataCache) scheduleMetadataDetails(fullpath string, meta *Metadata) {

	go func() {

		defer cache.detailsWg.Done()

		cache.mgr.bakeSem.Acquire()
		defer cache.mgr.bakeSem.Release()

		err := cache.mgr.bakeMetadataDetails(fullpath, meta)
		if err != nil {
			logWarn("Failed to bake metadata details", fullpath, err)
		}

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

	logDebug(cmd)
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
