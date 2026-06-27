package skillopt

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Live-pairwise (#77a/#77b) packet contract. These types decode the blinded
// paired review packet and the SEPARATE secret unblinding map produced by the
// gitmoot-skillopt fork's `live-pairwise` mode (gitmoot_skillopt/pairwise.py:
// build_pairwise_packet / write_pairwise_artifacts). The blinded packet is
// human-visible and carries only anonymized A/B options per item; the secret map
// is the ONLY artifact that records which anonymized side is the promoted
// champion vs the candidate challenger. Ingestion (#77b) is the review-close step
// that consumes BOTH plus the reviewer's per-item pick and unblinds it back to a
// champion/challenger preference.
//
// Everything here is additive to ContractVersion=1: it adds no field to any wire
// package the optimizer reads; it only decodes the fork's review artifacts.
const (
	// PairwiseReviewPacketKind / PairwiseSecretMapKind are the `kind` tags #77a
	// stamps on the blinded packet and the secret map respectively.
	PairwiseReviewPacketKind = "gitmoot-skillopt-pairwise-review-packet"
	PairwiseSecretMapKind    = "gitmoot-skillopt-pairwise-secret-map"
	// PairwisePicksKind is the `kind` tag on the reviewer's recorded picks file.
	PairwisePicksKind = "gitmoot-skillopt-pairwise-picks"

	// PairwiseMode is the #77a mode string.
	PairwiseMode = "live-pairwise"

	// PairwiseChampionRole / PairwiseChallengerRole are the canonical role labels
	// the unblind resolves to. They MATCH the eval_review_option role/label the
	// Mode B recording path registers ("champion"/"challenger"), so an unblinded
	// winner label is directly accepted by store.validateRankedFeedbackEventOptions.
	PairwiseChampionRole   = "champion"
	PairwiseChallengerRole = "challenger"

	// pairwiseRolePromoted / pairwiseRoleCandidate are the secret-map mapping
	// values (#77a writes mapping[label] -> "promoted"|"candidate"). Promoted is
	// the champion; candidate is the challenger.
	pairwiseRolePromoted  = "promoted"
	pairwiseRoleCandidate = "candidate"
)

// PairwisePacketSide is one anonymized output (A or B) for an item in the blinded
// packet. It deliberately carries NO role-revealing field (the fork strips
// target_trace_path etc.); only the secret map knows which side is the champion.
type PairwisePacketSide struct {
	Label      string          `json:"label"`
	Response   string          `json:"response"`
	Failed     bool            `json:"failed"`
	FailReason string          `json:"fail_reason,omitempty"`
	TokenUsage json.RawMessage `json:"token_usage,omitempty"`
}

// PairwisePacketItem is one reviewed item: the prompt plus the two anonymized
// outputs.
type PairwisePacketItem struct {
	ItemID  string               `json:"item_id"`
	Title   string               `json:"title,omitempty"`
	Prompt  string               `json:"prompt,omitempty"`
	Outputs []PairwisePacketSide `json:"outputs"`
}

// PairwiseReviewPacket is the blinded, human-visible packet.
type PairwiseReviewPacket struct {
	Kind            string               `json:"kind"`
	ContractVersion int                  `json:"contract_version"`
	Mode            string               `json:"mode"`
	TemplateID      string               `json:"template_id"`
	BaseVersionID   string               `json:"base_version_id"`
	RunID           string               `json:"run_id"`
	Items           []PairwisePacketItem `json:"items"`
}

// PairwiseSecretItem is the per-item unblinding record. The mapping is the sole
// source of truth for which anonymized label is the promoted champion. The
// trace-path fields are admin/debug only and are never written into feedback.
type PairwiseSecretItem struct {
	ItemID             string            `json:"item_id"`
	ChampionLabel      string            `json:"champion_label"`
	ChallengerLabel    string            `json:"challenger_label"`
	Mapping            map[string]string `json:"mapping"`
	PromotedTracePath  string            `json:"promoted_trace_path,omitempty"`
	CandidateTracePath string            `json:"candidate_trace_path,omitempty"`
}

// PairwiseSecretMap is the SEPARATE secret unblinding artifact.
type PairwiseSecretMap struct {
	Kind                 string               `json:"kind"`
	ContractVersion      int                  `json:"contract_version"`
	RunID                string               `json:"run_id"`
	TemplateID           string               `json:"template_id"`
	ChampionRole         string               `json:"champion_role"`
	ChallengerRole       string               `json:"challenger_role"`
	PromotedContentHash  string               `json:"promoted_content_hash,omitempty"`
	CandidateContentHash string               `json:"candidate_content_hash,omitempty"`
	Items                []PairwiseSecretItem `json:"items"`
}

// PairwisePick is one reviewer preference: the anonymized label (A or B) the
// reviewer preferred for an item. The reviewer never sees the secret map, so the
// pick is recorded against the blinded label only.
type PairwisePick struct {
	ItemID string `json:"item_id"`
	Pick   string `json:"pick"`
}

// PairwisePicks is the reviewer's recorded picks over the blinded packet.
type PairwisePicks struct {
	Kind     string         `json:"kind,omitempty"`
	RunID    string         `json:"run_id,omitempty"`
	Reviewer string         `json:"reviewer,omitempty"`
	Picks    []PairwisePick `json:"picks"`
}

// ParsePairwiseReviewPacket decodes and validates the blinded packet, enforcing
// the kind tag and ContractVersion so a stale or foreign payload is rejected.
func ParsePairwiseReviewPacket(data []byte) (PairwiseReviewPacket, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return PairwiseReviewPacket{}, errors.New("pairwise review packet is empty")
	}
	var packet PairwiseReviewPacket
	if err := json.Unmarshal(data, &packet); err != nil {
		return PairwiseReviewPacket{}, fmt.Errorf("decode pairwise review packet: %w", err)
	}
	if packet.Kind != PairwiseReviewPacketKind {
		return PairwiseReviewPacket{}, fmt.Errorf("pairwise review packet kind must be %q, got %q", PairwiseReviewPacketKind, packet.Kind)
	}
	if packet.ContractVersion != ContractVersion {
		return PairwiseReviewPacket{}, fmt.Errorf("pairwise review packet contract_version must be %d, got %d", ContractVersion, packet.ContractVersion)
	}
	if strings.TrimSpace(packet.RunID) == "" {
		return PairwiseReviewPacket{}, errors.New("pairwise review packet run_id is required")
	}
	if len(packet.Items) == 0 {
		return PairwiseReviewPacket{}, errors.New("pairwise review packet has no items")
	}
	return packet, nil
}

// ParsePairwiseSecretMap decodes and validates the secret map.
func ParsePairwiseSecretMap(data []byte) (PairwiseSecretMap, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return PairwiseSecretMap{}, errors.New("pairwise secret map is empty")
	}
	var secret PairwiseSecretMap
	if err := json.Unmarshal(data, &secret); err != nil {
		return PairwiseSecretMap{}, fmt.Errorf("decode pairwise secret map: %w", err)
	}
	if secret.Kind != PairwiseSecretMapKind {
		return PairwiseSecretMap{}, fmt.Errorf("pairwise secret map kind must be %q, got %q", PairwiseSecretMapKind, secret.Kind)
	}
	if secret.ContractVersion != ContractVersion {
		return PairwiseSecretMap{}, fmt.Errorf("pairwise secret map contract_version must be %d, got %d", ContractVersion, secret.ContractVersion)
	}
	return secret, nil
}

// ParsePairwisePicks decodes the reviewer's picks. The picks JSON accepts either
// an array of {item_id, pick} objects under "picks" OR a bare object map of
// item_id -> pick under "picks"; both shapes are tolerated so a reviewer can hand
// back whichever is convenient.
func ParsePairwisePicks(data []byte) (PairwisePicks, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return PairwisePicks{}, errors.New("pairwise picks file is empty")
	}
	// First decode the envelope without the picks field so we can fall back to a
	// map shape for picks.
	var envelope struct {
		Kind     string          `json:"kind"`
		RunID    string          `json:"run_id"`
		Reviewer string          `json:"reviewer"`
		Picks    json.RawMessage `json:"picks"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return PairwisePicks{}, fmt.Errorf("decode pairwise picks: %w", err)
	}
	out := PairwisePicks{Kind: envelope.Kind, RunID: envelope.RunID, Reviewer: envelope.Reviewer}
	raw := strings.TrimSpace(string(envelope.Picks))
	if raw == "" {
		return PairwisePicks{}, errors.New("pairwise picks file has no picks")
	}
	switch raw[0] {
	case '[':
		var arr []PairwisePick
		if err := json.Unmarshal(envelope.Picks, &arr); err != nil {
			return PairwisePicks{}, fmt.Errorf("decode pairwise picks array: %w", err)
		}
		out.Picks = arr
	case '{':
		var byID map[string]string
		if err := json.Unmarshal(envelope.Picks, &byID); err != nil {
			return PairwisePicks{}, fmt.Errorf("decode pairwise picks map: %w", err)
		}
		for itemID, pick := range byID {
			out.Picks = append(out.Picks, PairwisePick{ItemID: itemID, Pick: pick})
		}
	default:
		return PairwisePicks{}, errors.New("pairwise picks must be an array or object")
	}
	if len(out.Picks) == 0 {
		return PairwisePicks{}, errors.New("pairwise picks file has no picks")
	}
	return out, nil
}

// PairwiseUnblindResult is one item's resolved preference after unblinding. When
// Err is non-nil the item could not be resolved (missing/garbage secret entry,
// missing pick, missing output) and MUST be reported per item WITHOUT aborting
// the rest of the import — the other items remain importable.
type PairwiseUnblindResult struct {
	ItemID             string
	Title              string
	Prompt             string
	Pick               string // normalized anonymized label (A/B) the reviewer chose
	WinnerLabel        string // PairwiseChampionRole or PairwiseChallengerRole
	LoserLabel         string
	ChampionResponse   string
	ChallengerResponse string
	Err                error
}

// normalizePairwiseLabel upper-cases and trims an A/B label so picks and
// secret-map labels compare consistently regardless of case.
func normalizePairwiseLabel(label string) string {
	return strings.ToUpper(strings.TrimSpace(label))
}

// roleForLabel resolves the canonical winner role (champion/challenger) for an
// anonymized label using ONLY the secret-map item. It prefers the explicit
// mapping (label -> promoted|candidate) and cross-checks champion_label /
// challenger_label; an inconsistency is an error rather than a guess so a
// corrupt/inverted secret entry can never silently flip the preference. This is
// the single load-bearing unblind: it must come solely from the secret map.
func roleForLabel(secret PairwiseSecretItem, label string) (string, error) {
	label = normalizePairwiseLabel(label)
	if label == "" {
		return "", errors.New("empty pick label")
	}
	championLabel := normalizePairwiseLabel(secret.ChampionLabel)
	challengerLabel := normalizePairwiseLabel(secret.ChallengerLabel)

	var roleFromMapping string
	if len(secret.Mapping) > 0 {
		// Mapping keys may be either case; build a normalized view.
		for rawLabel, rawRole := range secret.Mapping {
			if normalizePairwiseLabel(rawLabel) != label {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(rawRole)) {
			case pairwiseRolePromoted:
				roleFromMapping = PairwiseChampionRole
			case pairwiseRoleCandidate:
				roleFromMapping = PairwiseChallengerRole
			default:
				return "", fmt.Errorf("secret map label %q has unknown role %q", label, rawRole)
			}
		}
	}

	var roleFromLabels string
	switch label {
	case championLabel:
		if championLabel != "" {
			roleFromLabels = PairwiseChampionRole
		}
	case challengerLabel:
		if challengerLabel != "" {
			roleFromLabels = PairwiseChallengerRole
		}
	}
	// When the same label is both champion and challenger the secret map is
	// corrupt and the unblind is undefined.
	if championLabel != "" && championLabel == challengerLabel {
		return "", fmt.Errorf("secret map champion and challenger share label %q", championLabel)
	}

	switch {
	case roleFromMapping != "" && roleFromLabels != "" && roleFromMapping != roleFromLabels:
		return "", fmt.Errorf("secret map mapping and labels disagree for %q", label)
	case roleFromMapping != "":
		return roleFromMapping, nil
	case roleFromLabels != "":
		return roleFromLabels, nil
	default:
		return "", fmt.Errorf("secret map has no champion/challenger entry for label %q", label)
	}
}

// responseForLabel returns the packet output text for an anonymized label.
func responseForLabel(item PairwisePacketItem, label string) (string, bool) {
	label = normalizePairwiseLabel(label)
	for _, side := range item.Outputs {
		if normalizePairwiseLabel(side.Label) == label {
			return side.Response, true
		}
	}
	return "", false
}

// UnblindPairwisePacket joins the blinded packet, the secret map, and the
// reviewer's picks into one resolved preference per item. It is pure and total:
// per-item failures are captured in the result's Err (never panics, never returns
// a top-level error for a single bad item) so the caller can record the good
// items and report the bad ones individually. A result whose Err is nil carries a
// WinnerLabel of PairwiseChampionRole or PairwiseChallengerRole plus both option
// responses, ready for the Mode B recording path.
//
// The order of results follows the packet item order, so the output is
// deterministic.
func UnblindPairwisePacket(packet PairwiseReviewPacket, secret PairwiseSecretMap, picks PairwisePicks) []PairwiseUnblindResult {
	secretByID := make(map[string]PairwiseSecretItem, len(secret.Items))
	for _, item := range secret.Items {
		secretByID[strings.TrimSpace(item.ItemID)] = item
	}
	pickByID := make(map[string]string, len(picks.Picks))
	for _, pick := range picks.Picks {
		pickByID[strings.TrimSpace(pick.ItemID)] = pick.Pick
	}

	results := make([]PairwiseUnblindResult, 0, len(packet.Items))
	for _, item := range packet.Items {
		itemID := strings.TrimSpace(item.ItemID)
		result := PairwiseUnblindResult{ItemID: itemID, Title: item.Title, Prompt: item.Prompt}

		secretItem, ok := secretByID[itemID]
		if !ok {
			result.Err = fmt.Errorf("no secret map entry for item %q", itemID)
			results = append(results, result)
			continue
		}
		rawPick, ok := pickByID[itemID]
		if !ok || strings.TrimSpace(rawPick) == "" {
			result.Err = fmt.Errorf("no reviewer pick for item %q", itemID)
			results = append(results, result)
			continue
		}
		pick := normalizePairwiseLabel(rawPick)
		result.Pick = pick

		winnerRole, err := roleForLabel(secretItem, pick)
		if err != nil {
			result.Err = fmt.Errorf("unblind item %q: %w", itemID, err)
			results = append(results, result)
			continue
		}
		result.WinnerLabel = winnerRole
		if winnerRole == PairwiseChampionRole {
			result.LoserLabel = PairwiseChallengerRole
		} else {
			result.LoserLabel = PairwiseChampionRole
		}

		// Resolve both option responses from the blinded packet via the secret
		// map's champion/challenger labels. We need both labels present and both
		// outputs available so the recorded eval_review_options carry the correct
		// answer text under the correct role.
		championLabel := normalizePairwiseLabel(secretItem.ChampionLabel)
		challengerLabel := normalizePairwiseLabel(secretItem.ChallengerLabel)
		if championLabel == "" || challengerLabel == "" {
			result.Err = fmt.Errorf("secret map item %q missing champion/challenger label", itemID)
			results = append(results, result)
			continue
		}
		championResponse, okChampion := responseForLabel(item, championLabel)
		challengerResponse, okChallenger := responseForLabel(item, challengerLabel)
		if !okChampion || !okChallenger {
			result.Err = fmt.Errorf("item %q is missing an output for the champion or challenger side", itemID)
			results = append(results, result)
			continue
		}
		result.ChampionResponse = championResponse
		result.ChallengerResponse = challengerResponse
		results = append(results, result)
	}
	return results
}
