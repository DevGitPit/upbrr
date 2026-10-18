// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCollectionDescriptionsOnlyRenderSetLinks(t *testing.T) {
	record := api.ImageCollection{
		Fingerprint:        "set",
		ContentFingerprint: "content",
		Host:               "slowpics",
		URL:                "https://slow.pics/c/synthetic",
		Purpose:            api.ScreenshotPurposeComparison,
		Category:           "Comps",
		Label:              "Set A",
		AccountScope:       "anonymous",
		Completed:          true,
		Members:            []api.ImageCollectionMember{{Identity: "image", Label: "Image"}},
	}
	exact := &api.ExactMediaAssets{CollectionHost: "slowpics", Collections: []api.ImageCollection{record}}
	block, err := exactComparisonCollectionBlock(exact)
	if err != nil || strings.Count(block, "[url=") != 1 || strings.Contains(block, "[img]") {
		t.Fatalf("comparison block = %q %v", block, err)
	}
	record.Purpose, record.Category = api.ScreenshotPurposeAudioAnalysis, "Audio Plots"
	exact.Collections = []api.ImageCollection{record}
	exact.AudioTracks = []api.AudioDescriptionTrack{{
		Ordinal: 1,
		Images:  []api.ScreenshotImage{{Path: "unhosted", Purpose: api.ScreenshotPurposeAudioAnalysis}},
		Stats:   "Synthetic amplitude statistics",
	}}
	block, err = exactAudioDescriptionBlock("OPEN", exact)
	if err != nil || strings.Count(block, "[url=") != 1 || strings.Contains(block, "[img]") || !strings.Contains(block, "Synthetic amplitude statistics") {
		t.Fatalf("audio block = %q %v", block, err)
	}
	record.Purpose, record.Category = api.ScreenshotPurposeHDRAnalysis, "Grade Checks"
	exact.Collections = []api.ImageCollection{record}
	exact.HDRPlots = []api.HDRDescriptionPlot{{Label: "Grade evidence"}}
	block, err = exactHDRDescriptionBlock("OPEN", exact)
	if err != nil || strings.Count(block, "[url=") != 1 || strings.Contains(block, "[img]") {
		t.Fatalf("HDR block = %q %v", block, err)
	}
	record.Label = "[img]unsafe"
	exact.Collections = []api.ImageCollection{record}
	if _, err = exactHDRDescriptionBlock("OPEN", exact); err == nil {
		t.Fatal("unsafe label rendered")
	}
}

func TestResolveCollectionDescriptionPreservesUnownedComparisons(t *testing.T) {
	t.Parallel()
	const customBlock = "[spoiler=Comps]\nCustom comparison notes\n[url=https://example.invalid/custom-comparison]Custom set[/url]\n[/spoiler]"
	const currentBlock = "[spoiler=Comps]\n[url=https://slow.pics/c/synthetic]Set A[/url]\n[/spoiler]"
	const ordinaryBody = "Synthetic release notes"
	const flatBlock = "[spoiler=Comps]\n[img]https://images.example.invalid/comparison.png[/img]\n[/spoiler]"
	record := api.ImageCollection{
		Fingerprint:        "set",
		ContentFingerprint: "content",
		Host:               "slowpics",
		URL:                "https://slow.pics/c/synthetic",
		Purpose:            api.ScreenshotPurposeComparison,
		Category:           "Comps",
		Label:              "Set A",
		AccountScope:       "anonymous",
		Completed:          true,
		Members:            []api.ImageCollectionMember{{Identity: "image", Label: "Image"}},
	}
	for _, test := range []struct {
		name                   string
		host                   string
		noExact, noSets, final bool
		description            string
		wantCurrent            int
	}{
		{
			name:        "default without exact assets",
			noExact:     true,
			description: ordinaryBody + "\n\n" + customBlock,
		},
		{name: "unset collection host", description: ordinaryBody + "\n\n" + customBlock},
		{
			name:        "legacy flat host",
			noSets:      true,
			description: ordinaryBody + "\n\n" + customBlock + "\n\n" + flatBlock,
		},
		{
			name:        "no current comparison sets",
			host:        "slowpics",
			noSets:      true,
			description: ordinaryBody + "\n\n" + customBlock,
		},
		{
			name:        "mixed custom and current collection",
			host:        "slowpics",
			description: ordinaryBody + "\n\n" + customBlock + "\n\n" + currentBlock,
			wantCurrent: 1,
		},
		{
			name:        "repeated current block",
			host:        "slowpics",
			description: currentBlock + "\n\n" + ordinaryBody + "\n\n" + customBlock + "\n\n" + currentBlock,
			wantCurrent: 1,
		},
		{
			name:        "saved final override",
			host:        "slowpics",
			final:       true,
			description: currentBlock + "\n\n" + ordinaryBody + "\n\n" + customBlock + "\n\n" + currentBlock,
			wantCurrent: 2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			exact := &api.ExactMediaAssets{
				CollectionHost: test.host,
				Collections:    []api.ImageCollection{record},
				ComparisonSets: []api.ImageCollectionInput{{
					Generation: "generation",
					Purpose:    api.ScreenshotPurposeComparison,
					Category:   "Comps",
					Label:      "Set A",
				}},
			}
			if test.host == "" {
				exact.Collections = nil
			}
			if test.noSets {
				exact.ComparisonSets = nil
			}
			if test.noExact {
				exact = nil
			}
			subject := api.UploadSubject{
				SourcePath:          filepath.Join(t.TempDir(), "Synthetic.Release.2026-GRP.mkv"),
				DescriptionOverride: test.description,
				ExactMedia:          exact,
			}
			if test.final {
				subject.DescriptionOverride = ""
				subject.DescriptionGroupsFinal = true
				subject.DescriptionGroups = []api.DescriptionBuilderGroup{{
					GroupKey:    "unit3d",
					Trackers:    []string{"AITHER"},
					Description: test.description,
				}}
			}
			assets, err := ResolveDescriptionAssets(t.Context(), "AITHER", subject, &stubRepo{}, api.NopLogger{}, descriptionAssetsTestRegistry(t))
			if err != nil {
				t.Fatalf("resolve collection description: %v", err)
			}
			if strings.Count(assets.Description, customBlock) != 1 || !strings.Contains(assets.Description, ordinaryBody) {
				t.Fatalf("unrelated custom comparison or surrounding notes changed: %q", assets.Description)
			}
			if got := strings.Count(assets.Description, currentBlock); got != test.wantCurrent {
				t.Fatalf("current collection block count=%d want=%d: %q", got, test.wantCurrent, assets.Description)
			}
			if test.wantCurrent == 0 || test.final {
				if assets.Description != test.description {
					t.Fatalf("untouched description changed: got %q want %q", assets.Description, test.description)
				}
			}
			if assets.Final != test.final || !assets.Override {
				t.Fatalf("final=%v override=%v; want final=%v override=true", assets.Final, assets.Override, test.final)
			}
			if len(assets.Screenshots) != 0 || len(assets.MenuImages) != 0 || len(assets.Slots) != 0 {
				t.Fatalf("comparison link entered ordinary screenshot assets: %#v", assets)
			}
		})
	}
}
