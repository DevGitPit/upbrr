// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type collectionImageHostingService interface {
	UploadCollection(context.Context, api.ImageHostingSubject, api.ImageCollectionInput, bool) (*api.ImageCollection, error)
}

func (b workflowDescriptionBuilder) uploadDescriptionCollection(
	ctx context.Context,
	subject api.UploadSubject,
	trackerNames []string,
	input api.ImageCollectionInput,
) error {
	if len(input.Images) == 0 || len(trackerNames) == 0 {
		return nil
	}
	if b.media == nil {
		return errors.New("collection image hosting is unavailable")
	}
	for _, tracker := range trackerNames {
		allowed, err := trackers.CollectionImageHostAllowed(b.media.registry, b.config, tracker, subject.ImageHostOverrides)
		if err != nil {
			return fmt.Errorf("workflow collection hosting: %w", err)
		}
		if !allowed {
			return fmt.Errorf("slow.pics collection hosting is not allowed for %s; clear the analysis host preference or change the target tracker", tracker)
		}
	}
	service, ok := b.media.images.(collectionImageHostingService)
	if !ok {
		return errors.New("collection image hosting is unavailable")
	}
	result, err := service.UploadCollection(
		ctx,
		api.ImageHostingSubject{MediaBinding: subject.MediaBinding, SourcePath: subject.SourcePath},
		input,
		subject.ExactMedia.ImageHostUploadSkipped,
	)
	if err != nil {
		return fmt.Errorf("workflow collection hosting: %w", err)
	}
	if result != nil {
		subject.ExactMedia.Collections = append(subject.ExactMedia.Collections, *result)
	}
	return nil
}

func (b workflowDescriptionBuilder) uploadComparisonCollections(ctx context.Context, subject api.UploadSubject, trackerNames []string) error {
	exact := subject.ExactMedia
	if exact == nil || exact.CollectionHost != "slowpics" {
		return nil
	}
	trackerNames = hdrDescriptionTrackers(subject, trackerNames)
	if len(trackerNames) == 0 {
		return nil
	}
	groups := make(map[string]int)
	for _, image := range exact.Screenshots {
		if image.ComparisonGroup == "" {
			continue
		}
		index, exists := groups[image.ComparisonGroup]
		if !exists {
			index = len(exact.ComparisonSets)
			groups[image.ComparisonGroup] = index
			exact.ComparisonSets = append(exact.ComparisonSets, api.ImageCollectionInput{
				Generation: image.ComparisonGroup,
				Purpose:    api.ScreenshotPurposeComparison,
				Category:   "Comps",
				Label:      image.ComparisonLabel,
			})
		}
		set := &exact.ComparisonSets[index]
		if set.Label != image.ComparisonLabel || image.ComparisonCount < 1 {
			return errors.New("comparison grouping is incomplete; reattach the comparison input")
		}
		set.Images = append(set.Images, image)
		set.Labels = append(set.Labels, image.ComparisonMemberLabel)
	}
	for _, set := range exact.ComparisonSets {
		for _, image := range set.Images {
			if len(set.Images) != image.ComparisonCount {
				return errors.New("comparison set selection is incomplete; select every member or reattach the intended set")
			}
		}
	}
	for _, set := range exact.ComparisonSets {
		if err := b.uploadDescriptionCollection(ctx, subject, trackerNames, set); err != nil {
			return fmt.Errorf("workflow collection hosting: %w", err)
		}
	}
	// Grouped comparisons have only set links in collection mode, including explicit skip.
	paths := make(map[string]bool)
	for _, set := range exact.ComparisonSets {
		for _, image := range set.Images {
			paths[image.Path] = true
		}
	}
	exact.Screenshots = slices.DeleteFunc(exact.Screenshots, func(image api.ScreenshotImage) bool { return paths[image.Path] })
	exact.ScreenshotUploads = slices.DeleteFunc(exact.ScreenshotUploads, func(image api.UploadedImageLink) bool { return paths[image.ImagePath] })
	return nil
}

func (b workflowDescriptionBuilder) uploadAudioCollections(ctx context.Context, subject api.UploadSubject, trackerNames []string) error {
	trackerNames = hdrDescriptionTrackers(subject, trackerNames)
	exact := subject.ExactMedia
	for _, track := range exact.AudioTracks {
		if len(track.Images) == 0 {
			continue
		}
		generation, err := api.CanonicalWorkflowFingerprint(struct {
			Analysis *api.AudioAnalysisRef
			Ordinal  int
		}{exact.AudioAnalysis, track.Ordinal})
		if err != nil {
			return fmt.Errorf("workflow collection hosting: %w", err)
		}
		set := api.ImageCollectionInput{
			Generation: string(generation),
			Purpose:    api.ScreenshotPurposeAudioAnalysis,
			Category:   "Audio Plots",
			Label:      fmt.Sprintf("Audio track %d", track.Ordinal),
			Images:     track.Images,
		}
		for index := range track.Images {
			label := fmt.Sprintf("Audio track %d plot %d", track.Ordinal, index+1)
			if len(track.ImageLabels) == len(track.Images) {
				label = track.ImageLabels[index]
			}
			set.Labels = append(set.Labels, label)
		}
		if err := b.uploadDescriptionCollection(ctx, subject, trackerNames, set); err != nil {
			return fmt.Errorf("workflow collection hosting: %w", err)
		}
	}
	return nil
}

func (b workflowDescriptionBuilder) uploadHDRCollections(ctx context.Context, subject api.UploadSubject, trackerNames []string) error {
	exact := subject.ExactMedia
	var sets []api.ImageCollectionInput
	groups := make(map[string]int)
	for _, plot := range exact.HDRPlots {
		category := hdrCollectionCategory(plot.Kind)
		key := plot.TargetID + "|" + category + "|" + plot.AudioTrackID
		index, exists := groups[key]
		if !exists {
			generation, err := api.CanonicalWorkflowFingerprint(struct {
				Analysis *api.HDRAnalysisRef
				Group    string
			}{exact.HDRAnalysis, key})
			if err != nil {
				return fmt.Errorf("workflow collection hosting: %w", err)
			}
			index = len(sets)
			groups[key] = index
			sets = append(sets, api.ImageCollectionInput{
				Generation: string(generation),
				Purpose:    api.ScreenshotPurposeHDRAnalysis,
				Category:   category,
				Label:      api.CollectionLabel(plot.Label, category),
			})
		}
		sets[index].Images = append(sets[index].Images, plot.Image)
		sets[index].Labels = append(sets[index].Labels, api.CollectionLabel(plot.Label, fmt.Sprintf("Image %d", len(sets[index].Images))))
	}
	for _, set := range sets {
		if err := b.uploadDescriptionCollection(ctx, subject, trackerNames, set); err != nil {
			return fmt.Errorf("workflow collection hosting: %w", err)
		}
	}
	return nil
}

func hdrCollectionCategory(kind api.HDRArtifactKind) string {
	if kind.DecodedMode() == api.HDRDecodedAudio {
		return "Audio Plots"
	}
	//nolint:exhaustive // Unlisted existing video artifacts belong to the plots category.
	switch kind {
	case api.HDRArtifactAutomaticGrade, api.HDRArtifactAutomaticColour, api.HDRArtifactGradePlot, api.HDRArtifactColourPlot:
		return "Grade Checks"
	case api.HDRArtifactDVSceneSheet,
		api.HDRArtifactHDR10PlusSceneSheet,
		api.HDRArtifactBoundaryDVSceneSheet,
		api.HDRArtifactBoundaryHDR10PlusSceneSheet,
		api.HDRArtifactSceneContactSheet:
		return "Alignment Checks"
	default:
		return "Plots"
	}
}

func collectionDescriptionOutcome(err error, input, template api.WorkflowFingerprint) (api.DescriptionSet, error) {
	var outcome *api.ImageCollectionOutcomeError
	if !errors.As(err, &outcome) || outcome.AttemptID == "" {
		return api.DescriptionSet{}, err
	}
	failure, ok := api.AsOperationFailure(err)
	if !ok {
		return api.DescriptionSet{}, err
	}
	scope := api.ImageCollectionEffectScope(outcome.AttemptID)
	return api.DescriptionSet{
		InputFingerprint:    input,
		TemplateFingerprint: template,
		Status:              api.StageStatusBlocked,
		Failures:            []api.WorkflowFailure{{Failure: failure, Resource: scope}},
		RequiredActions: []api.RequiredAction{
			{
				Kind:          api.RequiredActionReconcileSubmission,
				Status:        api.RequiredActionStatusPending,
				EffectKind:    api.WorkflowExternalEffectImageHosting,
				EffectScopeID: scope,
				Prompt:        "Check slow.pics for this uncertain upload. Confirm only after verifying that no collection was created. This clears the block without uploading; retry description generation separately.",
				Options:       []api.RequiredActionOption{{Value: api.RequiredActionReconcileNotCompleted, Label: "I verified no collection was created"}},
			},
		},
	}, nil
}
