package http

import (
	"context"
	"net/http"
	"sort"
	"time"
)

const readyTimeout = 2 * time.Second

type ReadyCheck func(ctx context.Context) error

func readyz(checks map[string]ReadyCheck) http.HandlerFunc {
	names := make([]string, 0, len(checks))
	for n := range checks {
		names = append(names, n)
	}
	sort.Strings(names)
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
		defer cancel()
		failed := false
		result := make(map[string]string, len(names))
		for _, n := range names {
			if err := checks[n](ctx); err != nil {
				failed = true
				result[n] = "unavailable"
				LoggerFrom(r.Context()).Error("readiness check failed", "check", n, "error", err.Error())
				continue
			}
			result[n] = "ok"
		}
		status, label := http.StatusOK, "ok"
		if failed {
			status, label = http.StatusServiceUnavailable, "unavailable"
		}
		WriteJSON(w, status, map[string]any{"status": label, "checks": result})
	}
}
