package dns

import (
	"fmt"
	"sync"
	"time"

	"github.com/miekg/dns"
)

type cacheEntry struct {
	msg     *dns.Msg
	expires time.Time
}

// responseCache is a TTL-aware DNS response cache.
type responseCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	maxSize int
}

func newResponseCache(maxSize int) *responseCache {
	c := &responseCache{
		entries: make(map[string]*cacheEntry, maxSize),
		maxSize: maxSize,
	}
	go c.evictLoop()
	return c
}

func cacheKey(r *dns.Msg) string {
	if len(r.Question) == 0 {
		return ""
	}
	q := r.Question[0]
	return fmt.Sprintf("%s:%d", q.Name, q.Qtype)
}

// get returns a cached response with decremented TTLs, or nil on miss/expiry.
func (c *responseCache) get(r *dns.Msg) *dns.Msg {
	key := cacheKey(r)
	if key == "" {
		return nil
	}
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return nil
	}
	remaining := time.Until(entry.expires)
	if remaining <= 0 {
		return nil
	}
	resp := entry.msg.Copy()
	resp.Id = r.Id
	ttl := uint32(remaining.Seconds())
	for _, rr := range resp.Answer {
		rr.Header().Ttl = ttl
	}
	return resp
}

// set stores a response. Only caches successful responses with at least one answer.
func (c *responseCache) set(r *dns.Msg, resp *dns.Msg) {
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) == 0 {
		return
	}
	key := cacheKey(r)
	if key == "" {
		return
	}
	// Use minimum TTL from answer section.
	minTTL := uint32(300)
	for _, rr := range resp.Answer {
		if rr.Header().Ttl < minTTL {
			minTTL = rr.Header().Ttl
		}
	}
	if minTTL == 0 {
		return
	}
	entry := &cacheEntry{
		msg:     resp.Copy(),
		expires: time.Now().Add(time.Duration(minTTL) * time.Second),
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.maxSize {
		// Evict one expired entry; if none, skip caching.
		now := time.Now()
		for k, v := range c.entries {
			if now.After(v.expires) {
				delete(c.entries, k)
				break
			}
		}
		if len(c.entries) >= c.maxSize {
			return
		}
	}
	c.entries[key] = entry
}

// stats returns the current number of cached entries.
func (c *responseCache) stats() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// evictLoop purges expired entries every 60 seconds.
func (c *responseCache) evictLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		c.mu.Lock()
		for k, v := range c.entries {
			if now.After(v.expires) {
				delete(c.entries, k)
			}
		}
		c.mu.Unlock()
	}
}
