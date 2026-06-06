package dns

import (
	"fmt"
	"net/http"
	"sync/atomic"
)

// MetricsHandler returns an http.HandlerFunc that exposes DNS stats in
// Prometheus text format.
func (s *Server) MetricsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		queries := atomic.LoadUint64(&s.statsQueries)
		blocked := atomic.LoadUint64(&s.statsBlocked)
		rules := 0
		if s.blocklists != nil {
			rules = s.blocklists.Stats()
		}

		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "# HELP sinkhole_queries_total Total DNS queries processed.\n")
		fmt.Fprintf(w, "# TYPE sinkhole_queries_total counter\n")
		fmt.Fprintf(w, "sinkhole_queries_total %d\n", queries)
		fmt.Fprintf(w, "# HELP sinkhole_blocked_total Total blocked queries.\n")
		fmt.Fprintf(w, "# TYPE sinkhole_blocked_total counter\n")
		fmt.Fprintf(w, "sinkhole_blocked_total %d\n", blocked)
		fmt.Fprintf(w, "# HELP sinkhole_rules_active Active blocklist rules.\n")
		fmt.Fprintf(w, "# TYPE sinkhole_rules_active gauge\n")
		fmt.Fprintf(w, "sinkhole_rules_active %d\n", rules)
		fmt.Fprintf(w, "# HELP sinkhole_cache_entries Cached DNS responses.\n")
		fmt.Fprintf(w, "# TYPE sinkhole_cache_entries gauge\n")
		fmt.Fprintf(w, "sinkhole_cache_entries %d\n", s.cache.stats())
	}
}
