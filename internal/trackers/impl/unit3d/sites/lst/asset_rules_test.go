// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSourceBoundAssetReview(t *testing.T) {
	s := lstValidationSubject()
	s.LanguageFacts.Tracks = nil
	s.ReleaseFeatures = []api.ReleaseFeature{api.ReleaseFeatureHybrid}
	s.HDRFacts = api.HDRFacts{Formats: []api.HDRFormat{api.HDRFormatDolbyVision}, Status: api.HDREvidenceComplete}
	s.QuestionnaireAnswers = map[string]string{}
	for _, field := range assetReviewQuestions(s) {
		value := "https://example.invalid/artifact"
		if field.Kind == "select" {
			value = "reviewed_source_match"
			if strings.Contains(field.Key, "screenshot_preservation") {
				value = "lossless_preserved"
			}
		}
		s.QuestionnaireAnswers[field.Key] = value
	}
	s.QuestionnaireAnswers[assetQuestionKey(s, "hybrid_review")] = "reviewed_source_match"
	if failures := assetReviewFailures(s); len(failures) != 0 {
		t.Fatalf("reviewed assets: %+v", failures)
	}
	if description := requiredAssetDescription(s); !strings.Contains(description, "example.invalid") {
		t.Fatalf("missing artifacts: %s", description)
	}
	s.PreparedResourceFingerprint = "changed"
	if failures := assetReviewFailures(s); len(failures) == 0 {
		t.Fatal("stale asset review reused")
	}
}

func TestScreenshotEvidenceDoesNotTrustCountOrExtension(t *testing.T) {
	s := lstValidationSubject()
	s.LanguageFacts.Tracks = nil
	s.AssetFacts.Screenshots = api.AssetEvidence{
		Ready:  true,
		Count:  3,
		Status: api.MetadataEvidenceStatusComplete,
	}
	s.QuestionnaireAnswers = map[string]string{assetQuestionKey(s, "screenshot_preservation"): "lossless_preserved"}
	if failures := assetReviewFailures(s); len(failures) != 1 || failures[0].EvidenceStatus != api.MetadataEvidenceStatusPartial {
		t.Fatalf("unknown formats accepted: %+v", failures)
	}
	s.AssetFacts.Screenshots.InspectedFormatCount = 3
	s.AssetFacts.Screenshots.LosslessCount = 2
	s.QuestionnaireAnswers = map[string]string{assetQuestionKey(s, "screenshot_preservation"): "lossless_preserved"}
	if failures := assetReviewFailures(s); len(failures) != 1 || failures[0].EvidenceStatus != api.MetadataEvidenceStatusComplete {
		t.Fatalf("lossy format accepted: %+v", failures)
	}
}
