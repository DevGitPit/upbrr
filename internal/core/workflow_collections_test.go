// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type descriptionCollectionHostFake struct {
	descriptionAudioHostFake
	inputs []api.ImageCollectionInput
	skips  []bool
}

func (f *descriptionCollectionHostFake) UploadCollection(_ context.Context, _ api.ImageHostingSubject, input api.ImageCollectionInput, skip bool) (*api.ImageCollection, error) {
	f.inputs = append(f.inputs, input)
	f.skips = append(f.skips, skip)
	if skip {
		return nil, nil
	}
	record := &api.ImageCollection{
		Fingerprint:        strconv.Itoa(len(f.inputs)),
		ContentFingerprint: "content",
		Host:               "slowpics",
		URL:                "https://slow.pics/c/synthetic",
		Purpose:            input.Purpose,
		Category:           input.Category,
		Label:              input.Label,
		AccountScope:       "anonymous",
		Completed:          true,
	}
	for index := range input.Images {
		record.Members = append(record.Members, api.ImageCollectionMember{Identity: strconv.Itoa(index), Label: input.Labels[index]})
	}
	return record, nil
}

func collectionDescriptionBuilder(t *testing.T) (workflowDescriptionBuilder, *descriptionCollectionHostFake) {
	t.Helper()
	registry := trackers.NewRegistry()
	for _, name := range []string{"OPEN", "RESTRICTED"} {
		descriptor := trackers.Descriptor{
			Name:              name,
			Definition:        mediaImageHostDefinition(name),
			Family:            trackers.FamilyStandalone,
			BaseURL:           "https://synthetic.example.invalid",
			UploadContentMode: trackers.UploadContentModeScreenshots,
		}
		if name == "RESTRICTED" {
			descriptor.ImageHost = &trackers.ImageHostPolicy{AllowedHosts: []string{"imgbb"}}
		}
		if err := registry.RegisterDescriptor(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Config{ImageHosting: config.ImageHostingConfig{AnalysisHost: "slowpics", Host1: "imgbb"}}
	host := &descriptionCollectionHostFake{}
	return workflowDescriptionBuilder{config: cfg, media: &mediaModule{
		cfg:      cfg,
		registry: registry,
		images:   host,
		logger:   api.NopLogger{},
	}}, host
}

func TestComparisonCollectionsKeepWholeSetsAndLegacyRoute(t *testing.T) {
	builder, host := collectionDescriptionBuilder(t)
	root := t.TempDir()
	groupImage := func(name string) api.ScreenshotImage {
		return api.ScreenshotImage{
			Path:                  filepath.Join(root, name),
			Purpose:               api.ScreenshotPurposeFinal,
			ComparisonGroup:       "group-1",
			ComparisonCount:       2,
			ComparisonLabel:       "Comparison set",
			ComparisonMemberLabel: name,
		}
	}
	first, second := groupImage("A.png"), groupImage("B.png")
	legacy := api.ScreenshotImage{Path: filepath.Join(root, "legacy.png"), Purpose: api.ScreenshotPurposeFinal}
	subject := api.UploadSubject{ExactMedia: &api.ExactMediaAssets{CollectionHost: "slowpics", Screenshots: []api.ScreenshotImage{first, second, legacy}}}
	if err := builder.uploadComparisonCollections(t.Context(), subject, []string{"OPEN"}); err != nil {
		t.Fatal(err)
	}
	if len(host.inputs) != 1 || len(host.inputs[0].Images) != 2 || host.inputs[0].Images[0].Path != first.Path || len(subject.ExactMedia.Collections) != 1 || len(subject.ExactMedia.Screenshots) != 1 || subject.ExactMedia.Screenshots[0].Path != legacy.Path {
		t.Fatalf("collection route = %#v %#v", host.inputs, subject.ExactMedia)
	}
	subject.ExactMedia = &api.ExactMediaAssets{CollectionHost: "slowpics", Screenshots: []api.ScreenshotImage{first}}
	if err := builder.uploadComparisonCollections(t.Context(), subject, []string{"OPEN"}); err == nil || len(host.inputs) != 1 {
		t.Fatal("partial group was uploaded")
	}
	subject.ExactMedia = &api.ExactMediaAssets{CollectionHost: "slowpics", Screenshots: []api.ScreenshotImage{first, second}}
	if err := builder.uploadComparisonCollections(t.Context(), subject, []string{"RESTRICTED"}); err != nil || len(host.inputs) != 2 || len(subject.ExactMedia.Collections) != 1 {
		t.Fatalf("restricted tracker collection = %#v, %v", host.inputs, err)
	}
}

func TestCollectionSkipPreservesStatsAndNeverOrdinaryUploads(t *testing.T) {
	builder, host := collectionDescriptionBuilder(t)
	image := api.ScreenshotImage{Path: filepath.Join(t.TempDir(), "waveform.png"), Purpose: api.ScreenshotPurposeAudioAnalysis}
	subject := api.UploadSubject{ExactMedia: &api.ExactMediaAssets{
		CollectionHost:         "slowpics",
		ImageHostUploadSkipped: true,
		AudioAnalysis:          &api.AudioAnalysisRef{ID: "audio", Revision: 1},
		AudioTracks: []api.AudioDescriptionTrack{{
			Ordinal: 1,
			Images:  []api.ScreenshotImage{image},
			Stats:   "Synthetic statistics",
		}},
	}}
	if err := builder.uploadAudioDescriptionImages(t.Context(), subject, []string{"OPEN"}); err != nil {
		t.Fatal(err)
	}
	if len(host.inputs) != 1 || !host.skips[0] || len(host.images) != 0 || len(subject.ExactMedia.Collections) != 0 || subject.ExactMedia.AudioTracks[0].Stats != "Synthetic statistics" {
		t.Fatal("skip or statistics changed")
	}
	image.Purpose = api.ScreenshotPurposeFinal
	image.ComparisonGroup = "comparison"
	image.ComparisonCount = 1
	result, err := builder.media.uploadImagesToTargetsWithFallback(t.Context(), api.UploadSubject{}, "", nil, []trackers.ImageUploadTarget{{Host: "imgbb", Trackers: []string{"OPEN"}}}, []api.ScreenshotImage{image}, nil, nil)
	if err != nil || len(result.Links) != 0 || len(host.images) != 0 {
		t.Fatalf("collection image escaped ordinary fallback: %#v %v", result, err)
	}
	if _, err := builder.media.uploadImagesToTarget(t.Context(), api.UploadSubject{}, trackers.ImageUploadTarget{Host: "slowpics"}, []api.ScreenshotImage{image}, nil, nil, false); err == nil {
		t.Fatal("ordinary target accepted slowpics")
	}
}

func TestCollectionDescriptionOutcomeRequiresExactConfirmation(t *testing.T) {
	cause := api.NewOperationError(api.OperationFailure{
		Code:      api.OperationFailureUnknownOutcome,
		Operation: api.OperationKindImageHosting,
		Recovery:  api.OperationRecoveryConfirm,
		Message:   "Uncertain collection",
	}, nil)
	outcome := &api.ImageCollectionOutcomeError{AttemptID: "exact-attempt", Cause: cause}
	descriptions, err := collectionDescriptionOutcome(outcome, "input", "template")
	if err != nil || descriptions.Status != api.StageStatusBlocked || len(descriptions.RequiredActions) != 1 {
		t.Fatalf("blocked description = %#v, %v", descriptions, err)
	}
	action := descriptions.RequiredActions[0]
	if action.Kind != api.RequiredActionReconcileSubmission || action.EffectKind != api.WorkflowExternalEffectImageHosting || action.EffectScopeID != api.ImageCollectionEffectScope("exact-attempt") || len(action.Options) != 1 || action.Options[0].Value != api.RequiredActionReconcileNotCompleted || action.Options[0].Label != "I verified no collection was created" {
		t.Fatalf("confirmation = %#v", action)
	}
	if descriptions.InputFingerprint != "input" || descriptions.TemplateFingerprint != "template" || len(descriptions.Failures) != 1 || descriptions.Failures[0].Resource != action.EffectScopeID {
		t.Fatalf("lost description authority: %#v", descriptions)
	}
	_, err = collectionDescriptionOutcome(cause, "input", "template")
	if !errors.Is(err, cause) {
		t.Fatalf("unscoped error became actionable: %v", err)
	}
}

func TestDescriptionCollectionsIgnoreScreenshotHostPolicy(t *testing.T) {
	for _, purpose := range []api.ScreenshotPurpose{api.ScreenshotPurposeComparison, api.ScreenshotPurposeHDRAnalysis, api.ScreenshotPurposeAudioAnalysis} {
		for _, failed := range []bool{false, true} {
			t.Run(string(purpose)+"/failed="+strconv.FormatBool(failed), func(t *testing.T) {
				builder, host := collectionDescriptionBuilder(t)
				image := api.ScreenshotImage{Path: filepath.Join(t.TempDir(), "plot.png"), Purpose: purpose}
				subject := api.UploadSubject{ExactMedia: &api.ExactMediaAssets{CollectionHost: "slowpics"}}
				if failed {
					subject.ImageHostOverrides.FailedHosts = []string{"slowpics", "imgbb"}
				}
				var err error
				switch purpose {
				case api.ScreenshotPurposeComparison:
					image.Purpose = api.ScreenshotPurposeFinal
					image.ComparisonGroup, image.ComparisonCount = "comparison", 1
					image.ComparisonLabel, image.ComparisonMemberLabel = "Comparison", "Source"
					subject.ExactMedia.Screenshots = []api.ScreenshotImage{image}
					err = builder.uploadComparisonCollections(t.Context(), subject, []string{"OPEN", "RESTRICTED"})
				case api.ScreenshotPurposeHDRAnalysis:
					subject.ExactMedia.HDRPlots = []api.HDRDescriptionPlot{{TargetID: "source", Image: image}}
					err = builder.uploadHDRDescriptionImages(t.Context(), subject, []string{"OPEN", "RESTRICTED"})
				case api.ScreenshotPurposeAudioAnalysis:
					subject.ExactMedia.AudioTracks = []api.AudioDescriptionTrack{{Ordinal: 1, Images: []api.ScreenshotImage{image}}}
					err = builder.uploadAudioDescriptionImages(t.Context(), subject, []string{"OPEN", "RESTRICTED"})
				case api.ScreenshotPurposePreview, api.ScreenshotPurposeFinal, api.ScreenshotPurposeMenu:
					t.Fatalf("unexpected purpose %q", purpose)
				}
				if err != nil {
					t.Fatal(err)
				}
				if len(host.inputs) != 1 || len(host.images) != 0 || len(subject.ExactMedia.Collections) != 1 || subject.ExactMedia.Collections[0].URL != "https://slow.pics/c/synthetic" {
					t.Fatalf("description collection = %#v, ordinary uploads = %#v, media = %#v", host.inputs, host.images, subject.ExactMedia)
				}
				for _, test := range []struct {
					host string
					want bool
				}{{"imgbb", !failed}, {"imgbox", false}, {"slowpics", false}} {
					allowed, policyErr := trackers.ReusableImageHostAllowedWithRegistry(builder.media.registry, builder.config, "RESTRICTED", test.host, subject.ImageHostOverrides)
					if policyErr != nil || allowed != test.want {
						t.Fatalf("ordinary host %s allowed = %t, err = %v, want %t", test.host, allowed, policyErr, test.want)
					}
				}
			})
		}
	}
}

func TestAnalysisImagesWithoutCollectionsKeepHostRestrictions(t *testing.T) {
	for _, audio := range []bool{false, true} {
		t.Run("audio="+strconv.FormatBool(audio), func(t *testing.T) {
			builder, host := collectionDescriptionBuilder(t)
			image := api.ScreenshotImage{Path: filepath.Join(t.TempDir(), "plot.png"), Purpose: api.ScreenshotPurposeHDRAnalysis}
			subject := api.UploadSubject{
				ImageHostOverrides: api.ImageHostOverrides{FailedHosts: []string{"imgbb"}},
				ExactMedia:         &api.ExactMediaAssets{},
			}
			var err error
			if audio {
				image.Purpose = api.ScreenshotPurposeAudioAnalysis
				subject.ExactMedia.AudioTracks = []api.AudioDescriptionTrack{{Ordinal: 1, Images: []api.ScreenshotImage{image}}}
				err = builder.uploadAudioDescriptionImages(t.Context(), subject, []string{"RESTRICTED"})
			} else {
				subject.ExactMedia.HDRPlots = []api.HDRDescriptionPlot{{TargetID: "source", Image: image}}
				err = builder.uploadHDRDescriptionImages(t.Context(), subject, []string{"RESTRICTED"})
			}
			if err == nil || len(host.inputs) != 0 || len(host.images) != 0 {
				t.Fatalf("restricted ordinary route: err = %v, collections = %#v, images = %#v", err, host.inputs, host.images)
			}
		})
	}
}
