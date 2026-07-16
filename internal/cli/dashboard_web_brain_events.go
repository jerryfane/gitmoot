package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gitmoot/gitmoot/internal/db"
)

const dashboardBrainEventsMaxLimit = 200

type dashboardBrainEvent struct {
	ID        int64           `json:"id"`
	At        string          `json:"at"`
	Kind      string          `json:"kind"`
	MemoryID  int64           `json:"memoryId,omitempty"`
	Key       string          `json:"key"`
	OwnerKind string          `json:"ownerKind"`
	OwnerRef  string          `json:"ownerRef"`
	Repo      string          `json:"repo,omitempty"`
	Scope     string          `json:"scope"`
	Actor     string          `json:"actor"`
	Detail    json.RawMessage `json:"detail"`
}

type dashboardBrainEventsResponse struct {
	Events     []dashboardBrainEvent `json:"events"`
	NextCursor int64                 `json:"nextCursor"`
}

func (d *webDataSource) BrainEvents(ctx context.Context, cursor int64, limit int) (dashboardBrainEventsResponse, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > dashboardBrainEventsMaxLimit {
		limit = dashboardBrainEventsMaxLimit
	}
	out := dashboardBrainEventsResponse{Events: []dashboardBrainEvent{}}
	err := withStore(d.home, func(store *db.Store) error {
		events, err := store.ListMemoryEvents(ctx, db.MemoryEventFilter{BeforeID: cursor, Limit: limit + 1})
		if err != nil {
			return err
		}
		if len(events) > limit {
			out.NextCursor = events[limit-1].ID
			events = events[:limit]
		}
		for _, event := range events {
			detail := json.RawMessage(event.Detail)
			if len(detail) == 0 || !json.Valid(detail) {
				detail = json.RawMessage(`{}`)
			}
			out.Events = append(out.Events, dashboardBrainEvent{ID: event.ID, At: event.At, Kind: event.Kind,
				MemoryID: event.MemoryID, Key: event.Key, OwnerKind: event.OwnerKind, OwnerRef: event.OwnerRef,
				Repo: event.Repo, Scope: event.Scope, Actor: event.Actor, Detail: detail})
		}
		return nil
	})
	return out, err
}

func (d *webDataSource) handleBrainEvents(w http.ResponseWriter, r *http.Request) {
	cursor, err := dashboardBrainEventIntQuery(r, "cursor", 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	limit, err := dashboardBrainEventIntQuery(r, "limit", 50)
	if err != nil || limit <= 0 {
		http.Error(w, "limit must be a positive integer", http.StatusBadRequest)
		return
	}
	response, err := d.BrainEvents(r.Context(), cursor, int(limit))
	if err != nil {
		http.Error(w, "brain events unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		return
	}
}

func dashboardBrainEventIntQuery(r *http.Request, name string, fallback int64) (int64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	return value, nil
}
