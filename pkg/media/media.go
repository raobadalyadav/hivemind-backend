// Package media holds the types shared between the media service and every
// feature that attaches uploaded photos/videos (stories, posts, chat, profile,
// plan covers). It is a leaf package so none of them import each other.
package media

import (
	"context"
	"errors"
)

// ErrNotFound covers "no such upload" AND "not yours" — the caller can't tell
// them apart, so upload ids can't be probed.
var ErrNotFound = errors.New("media: not found")

type Asset struct {
	ID         string
	URL        string
	ThumbURL   string
	Kind       string // image | video
	Width      int32
	Height     int32
	DurationMS int32
}

// Resolver turns upload ids the caller supplied into assets, verifying each
// belongs to ownerID. Order follows ids; duplicates are rejected.
type Resolver interface {
	Claim(ctx context.Context, ownerID string, ids []string) ([]Asset, error)
}
