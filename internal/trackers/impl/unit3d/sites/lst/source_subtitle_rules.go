// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func remuxSubtitleQuestions(subject api.TrackerValidationSubject) []api.TrackerQuestionnaireField {
	if !strings.EqualFold(subject.Type, "REMUX") {
		return nil
	}
	resources := remuxSubtitleResources(subject)
	var fields []api.TrackerQuestionnaireField
	for _, resource := range resources {
		for _, kind := range []string{"english", "forced_english"} {
			options := []string{"unresolved", "retail_track_retained", "not_available_on_retail", "silent_content", "required_track_missing"}
			if kind == "forced_english" {
				options = append(options, "retail_hardcoded_preserved")
			}
			key := sourceQuestionKey(subject, "retail_subtitle_"+kind+"_"+resource)
			value := subject.QuestionnaireAnswers[key]
			fields = append(
				fields,
				api.TrackerQuestionnaireField{
					Key:         key,
					Label:       "Retail " + strings.ReplaceAll(kind, "_", " ") + " subtitle coverage for resource " + resource,
					Kind:        "select",
					Options:     options,
					Required:    true,
					Value:       value,
					NeedsReview: value == "" || value == "unresolved",
					Help:        "Compare this exact resource with the retail source. Confirm retained applicable subtitle content, source unavailability, or the stated exception. A forced flag alone does not prove dialogue coverage; subtitle ordering is optional.",
				},
			)
		}
	}
	return fields
}

func remuxSubtitleResources(subject api.TrackerValidationSubject) []string {
	resources := []string{}
	for _, file := range subject.MediaFileFacts.Files {
		if !slices.Contains(resources, file.ResourceID) {
			resources = append(resources, file.ResourceID)
		}
	}
	for _, track := range subject.LanguageFacts.Tracks {
		if !slices.Contains(resources, track.ResourceID) {
			resources = append(resources, track.ResourceID)
		}
	}
	if len(resources) == 0 {
		resources = append(resources, "")
	}
	slices.Sort(resources)
	return resources
}

func remuxSubtitleFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	fields := remuxSubtitleQuestions(subject)
	failures := make([]api.RuleFailure, 0, len(fields))
	resources := remuxSubtitleResources(subject)
	for i, field := range fields {
		value := field.Value
		outcome := trackers.LanguageUnresolved
		switch {
		case value == "retail_track_retained" && !retainedEnglishSubtitle(subject, resources[i/2]):
			if subject.LanguageFacts.TrackCoverageComplete && !slices.ContainsFunc(subject.LanguageFacts.Tracks, func(track api.MediaTrackFacts) bool {
				return track.ResourceID == resources[i/2] && track.Kind == api.MediaTrackSubtitle && len(track.Languages) == 0
			}) {
				outcome = trackers.LanguageProhibited
			}
		case value == "required_track_missing":
			outcome = trackers.LanguageProhibited
		case slices.Contains(field.Options, value) && value != "unresolved":
			continue
		}
		failure := trackers.LanguageRuleFailure(subject, "retail_subtitles", field.Label+": source coverage requires review or correction", outcome)
		failure.QuestionnaireInput = true
		failures = append(failures, failure)
	}
	if len(failures) == 0 {
		return nil
	}
	return failures
}

func retainedEnglishSubtitle(subject api.TrackerValidationSubject, resource string) bool {
	return slices.ContainsFunc(subject.LanguageFacts.Tracks, func(track api.MediaTrackFacts) bool {
		return track.ResourceID == resource && track.Kind == api.MediaTrackSubtitle && slices.ContainsFunc(track.Languages, func(value string) bool {
			language, _ := languageutil.SubtitleLanguageParts(value)
			return language == "English"
		})
	})
}

func alternateMixQuestions(subject api.TrackerValidationSubject) []api.TrackerQuestionnaireField {
	var fields []api.TrackerQuestionnaireField
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackAudio || track.Role != api.AudioRoleAlternateMix {
			continue
		}
		key := trackers.LanguageQuestionKey(subject, "alternate_mix_"+track.ResourceID+"_"+track.ID)
		value := subject.QuestionnaireAnswers[key]
		fields = append(
			fields,
			api.TrackerQuestionnaireField{
				Key:         key,
				Label:       "Alternate mix source for resource " + track.ResourceID + ", track " + track.ID,
				Kind:        "select",
				Options:     []string{"unresolved", "additional_original_or_unique_remix", "not_unique"},
				Required:    subject.PersonalRelease,
				Value:       value,
				NeedsReview: value == "" || value == "unresolved",
				Help:        "Review the exact source mix and its distinguishing content; track labels alone do not establish uniqueness.",
			},
		)
	}
	return fields
}

func alternateMixFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	var failures []api.RuleFailure
	for _, field := range alternateMixQuestions(subject) {
		if field.Value == "additional_original_or_unique_remix" {
			continue
		}
		outcome := trackers.LanguageAdvisory
		if subject.PersonalRelease {
			outcome = trackers.LanguageUnresolved
			if field.Value == "not_unique" {
				outcome = trackers.LanguageProhibited
			}
		}
		failure := trackers.LanguageRuleFailure(subject, "alternate_mix", field.Label+": original-mix or unique-remix evidence is needed", outcome)
		failure.QuestionnaireInput = true
		if field.Value != "not_unique" {
			failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		}
		failures = append(failures, failure)
	}
	return failures
}
