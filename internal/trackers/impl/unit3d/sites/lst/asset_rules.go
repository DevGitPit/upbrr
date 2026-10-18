// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"crypto/sha256"
	"fmt"
	"html"
	"net/url"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func assetQuestionKey(subject api.TrackerValidationSubject, key string) string {
	if key == "hybrid_review" {
		refs := []string{
			subject.QuestionnaireAnswers[assetQuestionKey(subject, "hybrid_grade")],
			subject.QuestionnaireAnswers[assetQuestionKey(subject, "hybrid_plot")],
		}
		hash, _ := api.CanonicalWorkflowFingerprint(refs)
		key += "_" + string(hash)
	}
	return trackers.LanguageQuestionKey(
		subject,
		fmt.Sprintf("%s_%x", key, sha256.Sum256([]byte(fmt.Sprintf("%s|%#v|%#v", subject.PreparedResourceFingerprint, subject.HDRFacts, subject.AssetFacts)))),
	)
}

func hybridAssetsRequired(subject api.TrackerValidationSubject) bool {
	return slices.Contains(subject.ReleaseFeatures, api.ReleaseFeatureHybrid) &&
		(subject.HDRFacts.Status != api.HDREvidenceComplete || slices.Contains(subject.HDRFacts.Formats, api.HDRFormatDolbyVision) || slices.Contains(subject.HDRFacts.Formats, api.HDRFormatHDR10Plus))
}

func assetReviewQuestions(subject api.TrackerValidationSubject) []api.TrackerQuestionnaireField {
	var fields []api.TrackerQuestionnaireField
	if subject.AssetFacts.HostedScreenshots.Ready {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      assetQuestionKey(subject, "screenshot_preservation"),
			Label:    "Review hosted screenshot preservation",
			Kind:     "select",
			Options:  []string{"unresolved", "lossless_preserved", "lossy_or_changed"},
			Required: true,
			Help:     "Inspect the full-resolution hosted images against the prepared screenshots. Confirm their lossless format and preservation; a filename or thumbnail alone is insufficient.",
		})
	}
	if len(untaggedTracks(subject)) > 0 {
		fields = append(
			fields,
			api.TrackerQuestionnaireField{
				Key:      assetQuestionKey(subject, "track_notes"),
				Label:    "Clarify untagged tracks",
				Kind:     "text",
				Required: true,
				Help:     "Identify each untagged track and its language and role: " + strings.Join(untaggedTracks(subject), ", "),
			},
		)
	}
	if hybridAssetsRequired(subject) {
		fields = append(
			fields,
			api.TrackerQuestionnaireField{
				Key:      assetQuestionKey(subject, "hybrid_grade"),
				Label:    "Source-linked grade-check artifact",
				Kind:     "text",
				Required: true,
			},
			api.TrackerQuestionnaireField{
				Key:      assetQuestionKey(subject, "hybrid_plot"),
				Label:    "Source-linked HDR plot artifact",
				Kind:     "text",
				Required: true,
			},
			api.TrackerQuestionnaireField{
				Key:      assetQuestionKey(subject, "hybrid_review"),
				Label:    "Review hybrid evidence",
				Kind:     "select",
				Options:  []string{"unresolved", "reviewed_source_match", "source_mismatch"},
				Required: true,
				Help:     "Review both artifacts against this exact prepared source, including layer/base alignment. This records your review; it is not an automatic tool-specific grade.",
			},
		)
	}
	for i := range fields {
		value := strings.TrimSpace(subject.QuestionnaireAnswers[fields[i].Key])
		fields[i].Value = value
		fields[i].NeedsReview = value == "" || value == "unresolved" || value == "lossy_or_changed" || value == "source_mismatch"
	}
	return fields
}

func sourceArtifactURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}

func assetReviewFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	var failures []api.RuleFailure
	images := subject.AssetFacts.Screenshots
	if subject.AssetFacts.HostedScreenshots.Ready && images.LosslessCount < 3 {
		status := api.MetadataEvidenceStatusPartial
		if images.Ready && images.InspectedFormatCount == images.Count {
			status = api.MetadataEvidenceStatusComplete
		}
		failures = append(
			failures,
			trackers.NewEvidenceRuleFailure(
				"lst_screenshot_format",
				"three decoded lossless screenshots are required; unknown formats require inspection",
				api.RuleDispositionStrict,
				status,
			),
		)
	}
	for _, field := range assetReviewQuestions(subject) {
		value := strings.TrimSpace(subject.QuestionnaireAnswers[field.Key])
		valid := value != "" && value != "unresolved" && value != "lossy_or_changed" && value != "source_mismatch"
		if field.Kind == "text" {
			valid = value != ""
			if field.Key != assetQuestionKey(subject, "track_notes") {
				valid = sourceArtifactURL(value)
			}
		} else {
			valid = valid && slices.Contains(field.Options, value)
		}
		if valid {
			continue
		}
		failure := trackers.NewQuestionnaireRuleFailure("lst_asset_review", field.Label+" requires current source-bound evidence and review")
		if value == "lossy_or_changed" || value == "source_mismatch" {
			failure.EvidenceStatus = api.MetadataEvidenceStatusComplete
		} else {
			failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		}
		failures = append(failures, failure)
	}
	for i := range failures {
		failures[i].DebugBypass = true
	}
	return failures
}

func escapedDescriptionEvidence(value string) string {
	return strings.NewReplacer("[", "&#91;", "]", "&#93;").Replace(html.EscapeString(value))
}

func requiredAssetDescription(subject api.TrackerValidationSubject) string {
	var lines []string
	if documentation := presentationDescription(subject); documentation != "" {
		lines = append(lines, documentation)
	}
	if note := strings.TrimSpace(subject.QuestionnaireAnswers[assetQuestionKey(subject, "track_notes")]); note != "" {
		lines = append(lines, strings.NewReplacer("[", "&#91;", "]", "&#93;").Replace(html.EscapeString(note)))
	}
	if !hybridAssetsRequired(subject) {
		return strings.Join(lines, "\n")
	}
	for _, key := range []string{"hybrid_grade", "hybrid_plot"} {
		value := strings.TrimSpace(subject.QuestionnaireAnswers[assetQuestionKey(subject, key)])
		if sourceArtifactURL(value) {
			lines = append(lines, strings.NewReplacer("[", "&#91;", "]", "&#93;").Replace(html.EscapeString(value)))
		}
	}
	return strings.Join(lines, "\n")
}

func untaggedTracks(subject api.TrackerValidationSubject) []string {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	var tracks []string
	for _, track := range subject.LanguageFacts.Tracks {
		if (track.Kind == api.MediaTrackAudio && (track.Title == "" || len(track.Languages) == 0)) ||
			(track.Kind == api.MediaTrackSubtitle && len(track.Languages) == 0) {
			tracks = append(tracks, track.ResourceID+":"+track.ID)
		}
	}
	return tracks
}
