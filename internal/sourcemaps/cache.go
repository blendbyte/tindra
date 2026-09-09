package sourcemaps

import (
	"context"
	"os"
	"path/filepath"
	"time"
	"unsafe"
)

type fileFlight struct {
	done  chan struct{}
	lines []string
}

// The first reader owns the request. Waiters can leave independently; a
// canceled owner is not negative-cached and a subsequent reader may retry.
func (s *Store) sharedLines(ctx context.Context, url string) []string {
	if ctx.Err() != nil {
		return nil
	}
	s.fileMu.Lock()
	if entry, ok := s.fileCache[url]; ok {
		ttl := fileCacheTTL
		if entry.lines == nil {
			ttl = failureCacheTTL
		}
		if time.Since(entry.at) < ttl {
			s.fileMu.Unlock()
			return entry.lines
		}
	}
	if flight := s.flights[url]; flight != nil {
		s.fileMu.Unlock()
		select {
		case <-ctx.Done():
			return nil
		case <-flight.done:
			return flight.lines
		}
	}
	// Bound outbound work across simultaneous event responses, without a queue.
	if len(s.flights) >= 4 {
		s.fileMu.Unlock()
		return nil
	}
	flight := &fileFlight{done: make(chan struct{})}
	s.flights[url] = flight
	s.fileMu.Unlock()
	lines := s.fetchLines(ctx, url)
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	flight.lines = lines
	if ctx.Err() == nil {
		s.cacheLines(url, lines)
	}
	delete(s.flights, url)
	close(flight.done)
	return lines
}

// Caller holds fileMu. Entries are bounded by both count and accounted bytes.
func (s *Store) cacheLines(url string, lines []string) {
	size := len(url) + cap(lines)*int(unsafe.Sizeof(""))
	for _, line := range lines {
		size += len(line) + 1
	}
	if size > cacheBytesMax {
		return
	}
	if old, ok := s.fileCache[url]; ok {
		s.fileBytes -= old.bytes
	} else {
		s.fileOrder = append(s.fileOrder, url)
	}
	s.fileCache[url] = cachedFile{lines: lines, at: time.Now(), bytes: size}
	s.fileBytes += size
	for len(s.fileOrder) > fileCacheMax || s.fileBytes > cacheBytesMax {
		oldest := s.fileOrder[0]
		s.fileOrder = s.fileOrder[1:]
		s.fileBytes -= s.fileCache[oldest].bytes
		delete(s.fileCache, oldest)
	}
}

func (sm *SourceMap) cacheSize() int {
	size := int(unsafe.Sizeof(*sm)) + len(sm.Mappings)
	for _, values := range [][]string{sm.Sources, sm.SourcesContent, sm.Names} {
		size += cap(values) * int(unsafe.Sizeof(""))
		for _, value := range values {
			size += len(value)
		}
	}
	size += cap(sm.lines) * int(unsafe.Sizeof([]segment{}))
	for _, line := range sm.lines {
		size += cap(line) * int(unsafe.Sizeof(segment{}))
	}
	return size
}

func (s *Store) parsedSourceMap(ctx context.Context, projectID, hash string) *SourceMap {
	// Metadata is always read before this lookup. Replacement/deletion and
	// cross-instance changes therefore never resolve through stale metadata.
	key := projectID + "/" + hash
	if ctx.Err() != nil {
		return nil
	}
	s.parsedMu.Lock()
	cached := s.parsed[key]
	s.parsedMu.Unlock()
	if cached != nil {
		return cached
	}
	// Serialize cold parsing within each reader's response budget. Warm maps
	// remain available, and waiters recheck the cache after acquiring the slot.
	select {
	case s.parseGate <- struct{}{}:
	case <-ctx.Done():
		return nil
	}
	defer func() { <-s.parseGate }()
	s.parsedMu.Lock()
	cached = s.parsed[key]
	s.parsedMu.Unlock()
	if cached != nil {
		return cached
	}
	data, err := os.ReadFile(filepath.Join(s.dataDir, "sourcemaps", projectID, hash+".map"))
	if err != nil || ctx.Err() != nil {
		return nil
	}
	sm, err := ParseContext(ctx, data)
	if err != nil {
		return nil
	}
	size := sm.cacheSize() + len(key)
	if size > cacheBytesMax {
		return sm
	}
	s.parsedMu.Lock()
	defer s.parsedMu.Unlock()
	for len(s.parsedOrder) >= 64 || s.parsedBytes+size > cacheBytesMax {
		oldest := s.parsedOrder[0]
		s.parsedOrder = s.parsedOrder[1:]
		s.parsedBytes -= s.parsed[oldest].cacheSize() + len(oldest)
		delete(s.parsed, oldest)
	}
	s.parsed[key] = sm
	s.parsedOrder = append(s.parsedOrder, key)
	s.parsedBytes += size
	return sm
}
