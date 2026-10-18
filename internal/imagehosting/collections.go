// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehosting

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

const slowpicsAccountScope = "slowpics-anonymous-flat-v1-public-false-optimize-false"

type collectionRepository interface {
	ReserveImageCollection(context.Context, api.PreparedMediaBinding, api.ImageCollection) (*api.ImageCollection, bool, error)
	LoadImageCollection(context.Context, api.PreparedMediaBinding, string) (*api.ImageCollection, error)
	CompleteImageCollection(context.Context, api.PreparedMediaBinding, api.ImageCollection) error
	ReleaseImageCollection(context.Context, api.PreparedMediaBinding, api.ImageCollection) error
}

// UploadCollection hosts one exact permitted set. skip permits only complete
// cached results. Unknown outcomes remain durably fenced across process restarts.
func (s *Service) UploadCollection(
	ctx context.Context,
	subject api.ImageHostingSubject,
	input api.ImageCollectionInput,
	skip bool,
) (collection *api.ImageCollection, uploadErr error) {
	defer func() {
		if s == nil || s.logger == nil {
			return
		}
		switch {
		case uploadErr != nil:
			s.logger.Warnf("image hosting: collection upload blocked host=slowpics state=blocked")
		case collection == nil:
			s.logger.Debugf("image hosting: collection cache lookup host=slowpics state=skipped decision=cache_miss")
		default:
			s.logger.Debugf("image hosting: collection available host=slowpics state=completed decision=available")
		}
	}()

	if s == nil || !subject.MediaBinding.Valid() || s.cfg.ImageHosting.AnalysisHost != "slowpics" ||
		!input.Purpose.AllowsCollectionHosting() || !api.ValidImageCollectionCategory(input.Purpose, input.Category) || input.Generation == "" || len(input.Images) == 0 || len(input.Images) != len(input.Labels) {
		return nil, errors.New("image hosting: invalid collection authority")
	}
	repo, ok := s.repo.(collectionRepository)
	if !ok {
		return nil, errors.New("image hosting: durable collection storage is unavailable")
	}
	request := slowpicsCollectionRequest{Name: api.CollectionLabel(input.Label, "Image set")}
	for index, image := range input.Images {
		valid := image.Purpose == input.Purpose
		if input.Purpose == api.ScreenshotPurposeComparison {
			valid = image.Purpose == api.ScreenshotPurposeFinal && image.ComparisonGroup != "" &&
				image.ComparisonGroup == input.Images[0].ComparisonGroup && image.ComparisonCount == len(input.Images)
		}
		if !valid {
			return nil, errors.New("image hosting: collection member purpose is invalid")
		}
		request.Images = append(
			request.Images,
			slowpicsCollectionImage{Name: api.CollectionLabel(input.Labels[index], fmt.Sprintf("Image %d", index+1)), Path: image.Path},
		)
	}
	images, err := validateSlowpicsCollectionRequest(ctx, request)
	if err != nil {
		if skip && ctx.Err() == nil {
			return nil, nil
		}
		return nil, err
	}
	record := api.ImageCollection{
		Host:         "slowpics",
		Purpose:      input.Purpose,
		Category:     input.Category,
		Label:        request.Name,
		AccountScope: slowpicsAccountScope,
	}
	for index, image := range images {
		digest := sha256.Sum256(image.data)
		record.Members = append(record.Members, api.ImageCollectionMember{Identity: hex.EncodeToString(digest[:]), Label: request.Images[index].Name})
	}
	contentFingerprint, err := api.CanonicalWorkflowFingerprint(record)
	if err != nil {
		return nil, fmt.Errorf("image hosting: collection content fingerprint: %w", err)
	}
	record.ContentFingerprint = string(contentFingerprint)
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Binding    api.PreparedMediaBinding
		Generation string
		Collection api.ImageCollection
	}{subject.MediaBinding, input.Generation, record})
	if err != nil {
		return nil, fmt.Errorf("image hosting: collection fingerprint: %w", err)
	}
	record.Fingerprint = string(fingerprint)
	existing, err := repo.LoadImageCollection(ctx, subject.MediaBinding, record.Fingerprint)
	if err != nil {
		return nil, fmt.Errorf("image hosting: load collection: %w", err)
	}
	if existing != nil {
		if !existing.Completed {
			if skip {
				return nil, nil
			}
			return nil, collectionUnknownOutcome(existing)
		}
		if !collectionResultMatches(*existing, record) {
			return nil, errors.New("image hosting: stored collection is invalid")
		}
		return existing, nil
	}
	if skip {
		return nil, nil
	}
	record.AttemptID = rand.Text()
	receipt, err := api.BeginWorkflowExternalEffect(ctx, api.WorkflowExternalEffect{
		Kind:                api.WorkflowExternalEffectImageHosting,
		ScopeID:             api.ImageCollectionEffectScope(record.AttemptID),
		SemanticFingerprint: contentFingerprint,
	})
	if err != nil {
		return nil, fmt.Errorf("image hosting: fence collection attempt: %w", err)
	}
	record.EffectID = receipt.EffectID
	existing, acquired, err := repo.ReserveImageCollection(ctx, subject.MediaBinding, record)
	if err != nil || !acquired {
		if completeErr := api.CompleteWorkflowExternalEffect(context.WithoutCancel(ctx), receipt, false); completeErr != nil {
			return nil, fmt.Errorf("image hosting: finish unused collection attempt: %w", completeErr)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("image hosting: reserve collection: %w", err)
	}
	if !acquired {
		if existing != nil && collectionResultMatches(*existing, record) {
			return existing, nil
		}
		return nil, collectionUnknownOutcome(existing)
	}
	s.logger.Infof("image hosting: collection upload started host=slowpics purpose=%s members=%d", input.Purpose, len(images))
	uploader := slowpicsUploader{client: s.client}
	result, err := uploader.uploadValidatedCollection(ctx, request, images)
	if err != nil {
		if errors.Is(err, errSlowpicsOutcomeUnknown) {
			return nil, collectionUnknownOutcome(&record)
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if releaseErr := repo.ReleaseImageCollection(cleanupCtx, subject.MediaBinding, record); releaseErr != nil {
			return nil, collectionUnknownOutcome(&record)
		}
		return nil, err
	}
	record.URL, record.Completed = string(result), true
	if err := record.Validate(); err != nil {
		return nil, collectionUnknownOutcome(&record)
	}
	completionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := repo.CompleteImageCollection(completionCtx, subject.MediaBinding, record); err != nil {
		return nil, collectionUnknownOutcome(&record)
	}
	s.logger.Infof("image hosting: collection upload completed host=slowpics purpose=%s members=%d", input.Purpose, len(images))
	return &record, nil
}

func collectionUnknownOutcome(record *api.ImageCollection) error {
	failure := api.OperationFailure{
		Code:      api.OperationFailureUnknownOutcome,
		Operation: api.OperationKindImageHosting,
		Message:   "A slow.pics collection may already exist. Automatic recreation is blocked; verify the remote outcome before retrying.",
		Recovery:  api.OperationRecoveryNone,
	}
	attemptID := ""
	if record != nil && record.AttemptID != "" && record.EffectID != "" {
		attemptID = record.AttemptID
		failure.Recovery = api.OperationRecoveryConfirm
	} else {
		failure.Message = "A slow.pics collection may already exist. This earlier reservation lacks recovery authority and remains blocked."
	}
	return &api.ImageCollectionOutcomeError{AttemptID: attemptID, Cause: api.NewOperationError(failure, errSlowpicsOutcomeUnknown)}
}

func collectionResultMatches(existing, expected api.ImageCollection) bool {
	return existing.Validate() == nil && existing.Fingerprint == expected.Fingerprint && existing.ContentFingerprint == expected.ContentFingerprint &&
		existing.Host == expected.Host && existing.Purpose == expected.Purpose && existing.Category == expected.Category && existing.Label == expected.Label &&
		existing.AccountScope == expected.AccountScope && slices.Equal(existing.Members, expected.Members)
}
