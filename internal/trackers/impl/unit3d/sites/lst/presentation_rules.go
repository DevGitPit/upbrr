// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func presentationReviewQuestions(subject api.TrackerValidationSubject) []api.TrackerQuestionnaireField {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	fields := []api.TrackerQuestionnaireField{
		{
			Key:     "track_presentation",
			Label:   "Review track presentation",
			Kind:    "select",
			Options: []string{"unresolved", "reviewed_correct", "corrections_needed"},
			Help:    "Review each prepared resource: correct audio language/title tags, subtitle language and forced flags, other default flags, and reported bitrates. Subtitle ordering is optional. A description note does not correct an incorrect file tag.",
		},
	}
	switch strings.ToUpper(strings.TrimSpace(subject.Type)) {
	case "REMUX", "ENCODE":
		fields = append(
			fields,
			api.TrackerQuestionnaireField{
				Key:   "source_documentation",
				Label: "Source and processing references",
				Kind:  "text",
				Help:  "Identify the exact source releases and references to relevant comparisons, processing logs (including eac3to where applicable), and release notes. Explain any genuinely inapplicable item.",
			},
			api.TrackerQuestionnaireField{
				Key:     "documentation_review",
				Label:   "Review source documentation",
				Kind:    "select",
				Options: []string{"unresolved", "reviewed_complete", "missing_relevant_material"},
				Help:    "Review the entered source references, comparisons, logs and relevant notes against this exact prepared release. This records your review, not automatic source verification.",
			},
		)
	case "WEBDL", "WEB-DL":
		fields = append(
			fields,
			api.TrackerQuestionnaireField{
				Key:     "web_source_naming",
				Label:   "Review WEB source naming",
				Kind:    "select",
				Options: []string{"unresolved", "reviewed_correct", "corrections_needed"},
				Help:    "Confirm that the final release name accurately identifies the source service using your source records.",
			},
		)
	}
	for i := range fields {
		nameKey, _ := api.CanonicalWorkflowFingerprint(
			[]any{subject.FinalizedUploadName, subject.Release, subject.Service, subject.ServiceLongName, subject.ReleaseNameOverrides},
		)
		key := fields[i].Key
		if key == "documentation_review" {
			refs := subject.QuestionnaireAnswers[assetQuestionKey(subject, "presentation_source_documentation_"+string(nameKey))]
			refHash, _ := api.CanonicalWorkflowFingerprint(refs)
			key += "_" + string(refHash)
		}
		fields[i].Key = assetQuestionKey(subject, "presentation_"+key+"_"+string(nameKey))
		fields[i].Required = subject.PersonalRelease
		fields[i].Value = strings.TrimSpace(subject.QuestionnaireAnswers[fields[i].Key])
		fields[i].NeedsReview = fields[i].Value == "" || fields[i].Value == "unresolved"
	}
	return fields
}

func presentationReviewFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	var failures []api.RuleFailure
	if subject.PersonalRelease && len(untaggedTracks(subject)) > 0 {
		failures = append(
			failures,
			trackers.LanguageRuleFailure(
				subject,
				"presentation_tags",
				"correct missing file language/title tags for tracks: "+strings.Join(untaggedTracks(subject), ", "),
				trackers.LanguageProhibited,
			),
		)
	}
	for _, field := range presentationReviewQuestions(subject) {
		value := strings.TrimSpace(subject.QuestionnaireAnswers[field.Key])
		valid := field.Kind == "text" && value != "" ||
			field.Kind == "select" && slices.Contains(field.Options, value) && (value == "reviewed_correct" || value == "reviewed_complete")
		if valid {
			continue
		}
		outcome := trackers.LanguageAdvisory
		if subject.PersonalRelease {
			outcome = trackers.LanguageUnresolved
			if value == "corrections_needed" || value == "missing_relevant_material" {
				outcome = trackers.LanguageProhibited
			}
		}
		failure := trackers.LanguageRuleFailure(subject, "presentation_review", field.Label+": current source-bound review or correction is needed", outcome)
		failure.QuestionnaireInput = true
		if value == "" || value == "unresolved" {
			failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		}
		failures = append(failures, failure)
	}
	return failures
}

func presentationDescription(subject api.TrackerValidationSubject) string {
	for _, field := range presentationReviewQuestions(subject) {
		if field.Kind == "text" && field.Value != "" {
			return escapedDescriptionEvidence(field.Value)
		}
	}
	return ""
}
