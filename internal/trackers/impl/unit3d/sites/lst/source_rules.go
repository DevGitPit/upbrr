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

type audioDefect struct{ key, reason string }

// audioDefects identifies drawbacks without asserting that the conditional
// trumpability requirements or any independent prohibition have been satisfied.
func audioDefects(subject api.TrackerValidationSubject) []audioDefect {
	facts := subject.LanguageFacts
	var defects []audioDefect
	if facts.OriginalLanguagesKnown {
		for _, language := range facts.ProgrammeLanguages {
			if language == "English" || slices.Contains(facts.OriginalLanguages, language) {
				continue
			}
			key, label := "extra_dub", "Redundant Audio Tracks — extra programme dub: "
			if !facts.HasOriginalAudio() && !slices.Contains(facts.ProgrammeLanguages, "English") {
				key, label = "non_original_non_english_dub", "Non-Original Non-English Dub — programme audio contains only non-original, non-English dubs: "
			}
			defects = append(defects, audioDefect{key: key, reason: label + language})
		}
	}
	counts := map[string]map[string]int{}
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio {
			continue
		}
		if standaloneCompatibilityAudio(track) && len(trackers.KnownCompatibilityLanguages(track.Languages)) > 0 {
			candidates := compatibilityMixes(facts, track)
			mix := compatibilityMix(subject, track, candidates)
			if mix != "" && slices.ContainsFunc(candidates, func(candidate api.MediaTrackFacts) bool { return candidate.ID == mix && candidate.Codec != "" }) {
				continue
			}
		}
		switch track.Role {
		case api.AudioRoleCommentary,
			api.AudioRoleCompatibility,
			api.AudioRoleIsolatedScore,
			api.AudioRoleInterview,
			api.AudioRoleNovelty,
			api.AudioRoleAlternateMix:
			continue
		case api.AudioRoleProgramme:
			if counts[track.ResourceID] == nil {
				counts[track.ResourceID] = map[string]int{}
			}
			for _, language := range track.Languages {
				counts[track.ResourceID][language]++
				if counts[track.ResourceID][language] > 1 {
					defects = append(
						defects,
						audioDefect{
							key:    "redundant_programme",
							reason: "Redundant Audio Tracks — multiple programme tracks for " + language + " include " + track.ID + "; redundancy has not been verified",
						},
					)
				}
			}
		case api.AudioRoleDescription, api.AudioRoleVoiceOver, api.AudioRoleHistorical:
			defects = append(
				defects,
				audioDefect{
					key: "track_justification",
					reason: "Redundant Audio Tracks — secondary track " + track.ID + " (" + string(
						track.Role,
					) + "); redundancy has not been verified",
				},
			)
		}
	}
	return defects
}

func sourceLanguageFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	failures := alternateMixFailures(subject)
	failures = append(failures, remuxSubtitleFailures(subject)...)
	if needsSubtitlePresentation(subject) {
		reason := "English subtitle presentation and coverage have not been verified for every programme resource; external subtitles remain subject to an embedded-subtitle improvement"
		if hardcodedEnglishSubtitles(subject) {
			reason = "known burned-in English subtitles retain their hardcoded presentation; coverage of every programme resource has not been verified"
		}
		failure := trackers.LanguageRuleFailure(subject, "subtitle_presentation", reason, trackers.LanguageAdvisory)
		failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		failures = append(failures, failure)
	}
	return append(failures, compatibilityFailures(subject)...)
}

// Each represented programme resource needs known English programme audio or
// subtitles. Unrelated unknown tracks do not negate that positive evidence. Known
// burned-in English covers only the single resource inspected by production.
func needsSubtitlePresentation(subject api.TrackerValidationSubject) bool {
	facts := subject.LanguageFacts
	if !facts.OriginalLanguagesKnown || slices.Contains(facts.OriginalLanguages, "English") || slices.Contains(facts.OriginalLanguages, "ZXX") ||
		!slices.Contains(facts.SubtitleLanguages, "English") {
		return false
	}
	resources := map[string]bool{}
	for _, track := range facts.Tracks {
		if track.Kind == api.MediaTrackAudio && (track.Role == api.AudioRoleProgramme || track.Role == api.AudioRoleAlternateMix) {
			resources[track.ResourceID] = resources[track.ResourceID] || slices.Contains(track.Languages, "English")
		}
	}
	if len(resources) == 0 || !facts.TrackCoverageComplete {
		return true
	}
	if len(resources) == 1 && hardcodedEnglishSubtitles(subject) {
		return false
	}
	for resource, englishAudio := range resources {
		if englishAudio {
			continue
		}
		if !slices.ContainsFunc(facts.Tracks, func(track api.MediaTrackFacts) bool {
			return track.Kind == api.MediaTrackSubtitle && track.ResourceID == resource && slices.ContainsFunc(track.Languages, func(value string) bool {
				language, _ := languageutil.SubtitleLanguageParts(value)
				return language == "English"
			})
		}) {
			return true
		}
	}
	return false
}

func hardcodedEnglishSubtitles(subject api.TrackerValidationSubject) bool {
	return slices.ContainsFunc(subject.HardcodedSubtitleLanguages, func(value string) bool {
		language, _ := languageutil.SubtitleLanguageParts(value)
		return language == "English"
	}) || slices.ContainsFunc(subject.HardcodedSubtitleCoverage, func(coverage api.SubtitleLanguageCoverage) bool {
		return languageutil.NormalizeLanguageDisplay(coverage.Language) == "English"
	})
}

// standaloneCompatibilityAudio preserves LST's existing DD+ Atmos allowance
// while leaving the inspected codec and role unchanged.
func standaloneCompatibilityAudio(track api.MediaTrackFacts) bool {
	if strings.EqualFold(track.Codec, "DD+ Atmos") {
		track.Codec = "DD+"
	}
	return trackers.StandaloneDolbyAudio(track)
}

func compatibilityMixes(facts api.LanguageFacts, compatibility api.MediaTrackFacts) []api.MediaTrackFacts {
	return trackers.CompatibilityAudioMixes(facts, compatibility, true)
}

// compatibilityMix retains current manual answers, including explicit uncertainty.
func compatibilityMix(subject api.TrackerValidationSubject, track api.MediaTrackFacts, candidates []api.MediaTrackFacts) string {
	if answer, present := subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "compatibility_mix_"+track.ID)]; present {
		return answer
	}
	return trackers.AutomaticCompatibilityMix(track, candidates)
}

// Compatibility checks preserve measured codec and per-mix requirements.
// Required source provenance is reviewed independently of comparative quality.
func compatibilityFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	failures, _ := compatibilityAssessment(subject)
	return append(failures, compatibilitySourceFailures(subject)...)
}

func compatibilityAssessment(subject api.TrackerValidationSubject) ([]api.RuleFailure, bool) {
	facts := subject.LanguageFacts
	var failures []api.RuleFailure
	var companions []trackers.CompatibilityCompanion
	var required []api.MediaTrackFacts
	confirmed, pending := map[string]bool{}, map[string]bool{}
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackAudio {
			continue
		}
		if track.Codec == "" {
			failures = append(
				failures,
				trackers.LanguageRuleFailure(subject, "compatibility_evidence", "audio codec requires review for track "+track.ID, trackers.LanguageUnresolved),
			)
		}
		candidates := compatibilityMixes(facts, track)
		if track.Role != api.AudioRoleCompatibility &&
			(!standaloneCompatibilityAudio(track) || !slices.ContainsFunc(candidates, func(candidate api.MediaTrackFacts) bool { return candidate.Codec != "" })) {
			continue
		}
		answer := compatibilityMix(subject, track, candidates)
		if track.ID != "" && len(trackers.KnownCompatibilityLanguages(track.Languages)) > 0 &&
			(trackers.DolbyCompatibilityCodec(track.Codec) || strings.EqualFold(track.Codec, "DD+ Atmos")) {
			companion := trackers.CompatibilityCompanion{
				ID:         track.ID,
				ResourceID: track.ResourceID,
				CanCover:   true,
				Optional:   track.Role != api.AudioRoleCompatibility,
			}
			for _, candidate := range candidates {
				if candidate.ID != "" && candidate.Codec != "" {
					companion.Mixes = append(companion.Mixes, candidate.ID)
				}
			}
			companions = append(companions, companion)
		}
		if track.Role != api.AudioRoleCompatibility && answer == "not_compatibility" {
			continue
		}
		identified := track.ID != "" && len(trackers.KnownCompatibilityLanguages(track.Languages)) > 0
		if !identified {
			failures = append(
				failures,
				trackers.LanguageRuleFailure(
					subject,
					"compatibility_evidence",
					"compatibility track identity and language require review",
					trackers.LanguageUnresolved,
				),
			)
		}
		validCodec := trackers.DolbyCompatibilityCodec(track.Codec) || strings.EqualFold(track.Codec, "DD+ Atmos")
		if !validCodec {
			outcome := trackers.LanguageProhibited
			if track.Codec == "" {
				outcome = trackers.LanguageUnresolved
			}
			failures = append(
				failures,
				trackers.LanguageRuleFailure(subject, "compatibility_format", "unsupported compatibility audio format on track "+track.ID, outcome),
			)
		}
		failure := trackers.LanguageRuleFailure(
			subject,
			"compatibility_source",
			"compatibility track "+track.ID+" retains comparative-quality review: a demonstrably superior matching disc-derived companion can justify replacement; relative quality has not been verified",
			trackers.LanguageAdvisory,
		)
		failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		failures = append(failures, failure)
		if answer != "" &&
			slices.ContainsFunc(candidates, func(candidate api.MediaTrackFacts) bool { return candidate.ID == answer && candidate.Codec != "" }) {
			if validCodec && identified {
				confirmed[answer] = true
			} else {
				pending[answer] = true
			}
		} else {
			failure := trackers.LanguageRuleFailure(
				subject,
				"compatibility_mix",
				"source association with a TrueHD mix is unresolved for compatibility track "+track.ID,
				trackers.LanguageUnresolved,
			)
			failure.QuestionnaireInput = slices.ContainsFunc(
				candidates,
				func(candidate api.MediaTrackFacts) bool { return candidate.ID != "" && candidate.Codec != "" },
			)
			failures = append(failures, failure)
			for _, candidate := range candidates {
				pending[candidate.ID] = true
			}
		}
	}
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackAudio || track.Role == api.AudioRoleCompatibility || !strings.Contains(strings.ToLower(track.Codec), "truehd") ||
			track.EmbeddedCompatibility {
			continue
		}
		required = append(required, track)
		if confirmed[track.ID] {
			continue
		}
		outcome := trackers.LanguageProhibited
		if track.ID == "" || pending[track.ID] || subject.LanguageFacts.AudioStatus != api.MetadataEvidenceStatusComplete {
			outcome = trackers.LanguageUnresolved
		}
		failure := trackers.LanguageRuleFailure(
			subject,
			"compatibility_missing",
			"TrueHD mix "+track.ID+" requires its DD core or a permitted WEB compatibility substitute",
			outcome,
		)
		failure.QuestionnaireInput = track.ID != "" && pending[track.ID] && subject.LanguageFacts.AudioStatus == api.MetadataEvidenceStatusComplete
		failures = append(failures, failure)
	}
	review := facts.AudioStatus == api.MetadataEvidenceStatusComplete && trackers.ReviewCompatibilityAssignments(failures, required, companions, false)
	return failures, review
}

func compatibilitySourceQuestions(subject api.TrackerValidationSubject) []api.TrackerQuestionnaireField {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	var fields []api.TrackerQuestionnaireField
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackAudio || track.ID == "" || (!standaloneCompatibilityAudio(track) && track.Role != api.AudioRoleCompatibility) ||
			!trackers.DolbyCompatibilityCodec(track.Codec) && !strings.EqualFold(track.Codec, "DD+ Atmos") {
			continue
		}
		candidates := compatibilityMixes(subject.LanguageFacts, track)
		mix := compatibilityMix(subject, track, candidates)
		if !slices.ContainsFunc(candidates, func(candidate api.MediaTrackFacts) bool { return candidate.ID == mix && candidate.Codec != "" }) {
			continue
		}
		key := trackers.LanguageQuestionKey(subject, "compatibility_origin_"+track.ID+"_"+mix)
		options := []string{"unresolved", "matching_web_source"}
		if slices.Contains([]string{"DD", "AC-3", "AC3"}, strings.ToUpper(strings.TrimSpace(track.Codec))) {
			options = append(options, "matching_disc_core")
		}
		options = append(options, "other_source")
		value := subject.QuestionnaireAnswers[key]
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:         key,
			Label:       "Compatibility source for resource " + track.ResourceID + ", track " + track.ID + ", mix " + mix,
			Kind:        "select",
			Options:     options,
			Required:    true,
			Value:       value,
			Help:        "Verify this exact companion is the matching disc DD core or a matching WEB-sourced DD/DD+/DD+ Atmos substitute. Codec and language matching alone do not establish source provenance.",
			NeedsReview: value == "" || value == "unresolved" || value == "other_source",
		})
	}
	return fields
}

func compatibilitySourceFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	var failures []api.RuleFailure
	for _, field := range compatibilitySourceQuestions(subject) {
		value := subject.QuestionnaireAnswers[field.Key]
		if value != "unresolved" && value != "other_source" && slices.Contains(field.Options, value) {
			continue
		}
		outcome := trackers.LanguageUnresolved
		if value == "other_source" {
			outcome = trackers.LanguageProhibited
		}
		failure := trackers.LanguageRuleFailure(subject, "compatibility_origin", field.Label+": verify permitted source provenance", outcome)
		failure.QuestionnaireInput = true
		failures = append(failures, failure)
	}
	return failures
}
