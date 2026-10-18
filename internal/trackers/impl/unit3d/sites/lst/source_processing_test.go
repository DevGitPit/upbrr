// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"github.com/autobrr/upbrr/pkg/api"
	"slices"
	"strings"
	"testing"
)

func TestSourceProcessingReviewAllFormats(t *testing.T) {
	for _, kind := range []string{"ENCODE", "REMUX", "WEBDL", "DISC"} {
		for _, answer := range []string{"", "reviewed_no_user_processing_or_improper_splicing", "retail_processing_preserved", "user_generated_or_enhanced_streams", "improper_splicing"} {
			subject := lstValidationSubject()
			subject.Type = kind
			subject.QuestionnaireAnswers[sourceQuestionKey(subject, "stream_processing")] = answer
			failures := sourceValidationFailures(subject)
			failure, found := api.RuleFailure{}, false
			for _, f := range failures {
				if f.Rule == "lst_source_review" && strings.HasPrefix(f.Reason, "Review source stream processing") {
					failure = f
					found = true
				}
			}
			bad := slices.Contains([]string{"user_generated_or_enhanced_streams", "improper_splicing"}, answer)
			if found != (answer == "" || bad) {
				t.Fatalf("%s %q: %+v", kind, answer, failures)
			}
			if found && (failure.Disposition != api.RuleDispositionStrict || (failure.EvidenceStatus == api.MetadataEvidenceStatusComplete) != bad) {
				t.Fatalf("wrong evidence: %+v", failure)
			}
		}
	}
}
