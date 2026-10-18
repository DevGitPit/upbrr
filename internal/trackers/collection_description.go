// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func imageCollectionDescriptionLink(collection api.ImageCollection) (string, error) {
	if err := collection.Validate(); err != nil {
		return "", fmt.Errorf("collection description: %w", err)
	}
	return "[url=" + collection.URL + "]" + collection.Label + "[/url]", nil
}

func exactComparisonCollectionBlock(exact *api.ExactMediaAssets) (string, error) {
	if exact == nil || exact.CollectionHost != "slowpics" {
		return "", nil
	}
	var links []string
	for _, collection := range exact.Collections {
		if collection.Purpose != api.ScreenshotPurposeComparison {
			continue
		}
		link, err := imageCollectionDescriptionLink(collection)
		if err != nil {
			return "", fmt.Errorf("collection description: %w", err)
		}
		links = append(links, link)
	}
	if len(links) == 0 {
		return "", nil
	}
	return "[spoiler=Comps]\n" + strings.Join(links, "\n") + "\n[/spoiler]", nil
}
