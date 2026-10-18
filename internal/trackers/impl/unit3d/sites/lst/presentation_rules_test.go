// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"github.com/autobrr/upbrr/pkg/api"
	"strings"
	"testing"
)

func TestPresentationReviewPriorityAndGeneration(t *testing.T) {
	for _, personal := range []bool{false, true} {
		subject := lstValidationSubject()
		subject.PersonalRelease = personal
		subject.Type = "REMUX"
		subject.QuestionnaireAnswers = map[string]string{}
		for _, failure := range presentationReviewFailures(subject) {
			if (failure.Disposition == api.RuleDispositionStrict) != personal || failure.EvidenceStatus != api.MetadataEvidenceStatusPartial {
				t.Fatalf("priority or missing evidence: %+v", failure)
			}
		}
		for _, field := range presentationReviewQuestions(subject) {
			if field.Required != personal {
				t.Fatal("wrong required state")
			}
			value := "reviewed_correct"
			if strings.Contains(field.Key, "documentation_review") {
				value = "reviewed_complete"
			}
			if field.Kind == "text" {
				value = "Synthetic source; https://example.test/review/log and comparison; no additional notes apply"
			}
			subject.QuestionnaireAnswers[field.Key] = value
		}
		for _, field := range presentationReviewQuestions(subject) {
			if strings.Contains(field.Key, "documentation_review") {
				subject.QuestionnaireAnswers[field.Key] = "reviewed_complete"
			}
		}
		if failures := presentationReviewFailures(subject); len(failures) != 0 {
			t.Fatalf("reviewed: %+v", failures)
		}
		if !strings.Contains(requiredAssetDescription(subject), "https://example.test/review/log") {
			t.Fatal("reviewed references missing from final appendix")
		}
		subject.Identity.Generation++
		if len(presentationReviewFailures(subject)) == 0 {
			t.Fatal("stale review reused")
		}
	}
}

func TestPersonalPresentationCannotOverrideMissingTags(t *testing.T) {
	subject := lstValidationSubject()
	subject.PersonalRelease = true
	subject.LanguageFacts.Tracks[0].Title = ""
	for _, field := range presentationReviewQuestions(subject) {
		subject.QuestionnaireAnswers[field.Key] = "reviewed_correct"
	}
	if failures := presentationReviewFailures(subject); len(failures) == 0 || failures[0].Disposition != api.RuleDispositionStrict {
		t.Fatalf("missing file tag passed: %+v", failures)
	}
}
