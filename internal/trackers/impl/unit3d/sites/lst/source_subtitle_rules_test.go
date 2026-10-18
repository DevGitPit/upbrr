// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"github.com/autobrr/upbrr/pkg/api"
	"testing"
)

func TestLSTRetailSubtitleReview(t *testing.T) {
	subject := ordinaryCompatibilitySubject()
	subject.Type = "REMUX"
	subject.QuestionnaireAnswers = map[string]string{}
	fields := remuxSubtitleQuestions(subject)
	if len(fields) == 0 || len(remuxSubtitleFailures(subject)) != len(fields) {
		t.Fatal("missing source evidence passed")
	}
	for _, field := range fields {
		subject.QuestionnaireAnswers[field.Key] = "not_available_on_retail"
	}
	if failures := remuxSubtitleFailures(subject); len(failures) != 0 {
		t.Fatalf("source absence: %#v", failures)
	}
	for _, field := range fields {
		subject.QuestionnaireAnswers[field.Key] = "retail_track_retained"
	}
	if len(remuxSubtitleFailures(subject)) == 0 {
		t.Fatal("retained answer hid absent measured subtitles")
	}
	for _, field := range fields {
		subject.QuestionnaireAnswers[field.Key] = "silent_content"
	}
	if len(remuxSubtitleFailures(subject)) != 0 {
		t.Fatal("silent exception failed")
	}
	subject.QuestionnaireAnswers[fields[1].Key] = "retail_hardcoded_preserved"
	if len(remuxSubtitleFailures(subject)) != 0 {
		t.Fatal("retail hardcoded exception failed")
	}
	subject.QuestionnaireAnswers[fields[0].Key] = "required_track_missing"
	failures := remuxSubtitleFailures(subject)
	if len(failures) != 1 || failures[0].Disposition != api.RuleDispositionStrict || failures[0].EvidenceStatus != api.MetadataEvidenceStatusComplete {
		t.Fatalf("known omission: %#v", failures)
	}
	subject.Identity.Generation++
	if len(remuxSubtitleFailures(subject)) != len(fields) {
		t.Fatal("stale source review persisted")
	}
	subject.Type = "ENCODE"
	if len(remuxSubtitleQuestions(subject)) != 0 {
		t.Fatal("review leaked outside remux")
	}
}

func TestLSTAlternateMixPriority(t *testing.T) {
	subject := ordinaryCompatibilitySubject()
	subject.LanguageFacts.Tracks[0].Role = api.AudioRoleAlternateMix
	subject.QuestionnaireAnswers = map[string]string{}
	fields := alternateMixQuestions(subject)
	if len(fields) != 1 {
		t.Fatalf("fields: %#v", fields)
	}
	for _, personal := range []bool{false, true} {
		subject.PersonalRelease = personal
		fields = alternateMixQuestions(subject)
		if fields[0].Required != personal {
			t.Fatal("wrong priority")
		}
		failures := alternateMixFailures(subject)
		if len(failures) != 1 || (failures[0].Disposition == api.RuleDispositionStrict) != personal {
			t.Fatalf("priority: %#v", failures)
		}
		subject.QuestionnaireAnswers[fields[0].Key] = "additional_original_or_unique_remix"
		if len(alternateMixFailures(subject)) != 0 {
			t.Fatal("current review failed")
		}
		subject.QuestionnaireAnswers = map[string]string{}
	}
}

func TestLSTRetailSubtitleRetentionNeedsResourceEvidence(t *testing.T) {
	subject := ordinaryCompatibilitySubject()
	subject.Type = "REMUX"
	subject.LanguageFacts.TrackCoverageComplete = true
	resource := subject.LanguageFacts.Tracks[0].ResourceID
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
		ID:         "subtitle",
		ResourceID: resource,
		Kind:       api.MediaTrackSubtitle,
	})
	subject.QuestionnaireAnswers = map[string]string{}
	for _, field := range remuxSubtitleQuestions(subject) {
		subject.QuestionnaireAnswers[field.Key] = "retail_track_retained"
	}
	for _, failure := range remuxSubtitleFailures(subject) {
		if failure.EvidenceStatus != api.MetadataEvidenceStatusPartial {
			t.Fatalf("unknown language became violation: %#v", failure)
		}
	}
	subject.LanguageFacts.Tracks[len(subject.LanguageFacts.Tracks)-1].Languages = []string{"English"}
	for _, field := range remuxSubtitleQuestions(subject) {
		subject.QuestionnaireAnswers[field.Key] = "retail_track_retained"
	}
	if failures := remuxSubtitleFailures(subject); len(failures) != 0 {
		t.Fatalf("retained current evidence: %#v", failures)
	}
	subject.LanguageFacts.Tracks[len(subject.LanguageFacts.Tracks)-1].ResourceID = "other"
	if len(remuxSubtitleFailures(subject)) == 0 {
		t.Fatal("resource change preserved source review")
	}
}
