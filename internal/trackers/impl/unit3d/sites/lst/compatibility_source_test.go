// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestLSTCompatibilityRequiresCurrentSourceProvenance(t *testing.T) {
	for _, codec := range []string{"DD", "DD+", "DD+ Atmos"} {
		t.Run(codec, func(t *testing.T) {
			subject := ordinaryCompatibilitySubject()
			subject.LanguageFacts.Tracks[1].Codec = codec
			fields := compatibilitySourceQuestions(subject)
			if len(fields) != 1 || !fields[0].Required {
				t.Fatalf("fields = %#v", fields)
			}
			failures := compatibilityFailures(subject)
			if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
				return f.Rule == "language_compatibility_origin" && f.EvidenceStatus == api.MetadataEvidenceStatusPartial && f.Disposition == api.RuleDispositionStrict
			}) {
				t.Fatalf("missing provenance passed: %#v", failures)
			}
			subject.QuestionnaireAnswers = map[string]string{fields[0].Key: "matching_web_source"}
			for _, personal := range []bool{false, true} {
				subject.PersonalRelease = personal
				current := compatibilitySourceQuestions(subject)
				subject.QuestionnaireAnswers[current[0].Key] = "matching_web_source"
				if failures := compatibilitySourceFailures(subject); len(failures) != 0 {
					t.Fatalf("permitted WEB source personal=%t: %#v", personal, failures)
				}
			}
			subject.Identity.Generation++
			if failures := compatibilitySourceFailures(subject); len(failures) != 1 {
				t.Fatalf("stale provenance persisted: %#v", failures)
			}
			fields = compatibilitySourceQuestions(subject)
			subject.QuestionnaireAnswers[fields[0].Key] = "other_source"
			if failures := compatibilitySourceFailures(subject); len(failures) != 1 || failures[0].EvidenceStatus != api.MetadataEvidenceStatusComplete {
				t.Fatalf("other source = %#v", failures)
			}
		})
	}
}

func TestLSTCompatibilityCoreRequiresDDCodec(t *testing.T) {
	subject := ordinaryCompatibilitySubject()
	subject.LanguageFacts.Tracks[1].Codec = "DD+"
	fields := compatibilitySourceQuestions(subject)
	if len(fields) != 1 || slices.Contains(fields[0].Options, "matching_disc_core") {
		t.Fatalf("DD+ offered as DD core: %#v", fields)
	}
	subject.QuestionnaireAnswers = map[string]string{fields[0].Key: "matching_disc_core"}
	if failures := compatibilitySourceFailures(subject); len(failures) != 1 {
		t.Fatalf("DD+ core accepted: %#v", failures)
	}
	subject.LanguageFacts.Tracks[1].Codec = "DD"
	fields = compatibilitySourceQuestions(subject)
	subject.QuestionnaireAnswers[fields[0].Key] = "matching_disc_core"
	if failures := compatibilitySourceFailures(subject); len(failures) != 0 {
		t.Fatalf("matching DD core = %#v", failures)
	}
}
