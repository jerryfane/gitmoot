package skillopt

import (
	"encoding/json"
	"strings"
	"testing"
)

// pairwisePacketFixture builds a one-item blinded packet, the matching secret
// map, and a picks file with the given orientation. championIsA controls which
// anonymized label the champion (promoted) sits behind, so a single fixture
// exercises both A->champion and A->challenger orientations.
func pairwisePacketFixture(championIsA bool, pick string) (PairwiseReviewPacket, PairwiseSecretMap, PairwisePicks) {
	championLabel, challengerLabel := "A", "B"
	if !championIsA {
		championLabel, challengerLabel = "B", "A"
	}
	// Outputs are always presented as A then B; the response text is keyed to the
	// role so we can assert the unblind routed the correct answer to each role.
	championResponse := "CHAMPION-RESPONSE"
	challengerResponse := "CHALLENGER-RESPONSE"
	var sideAResponse, sideBResponse string
	if championIsA {
		sideAResponse, sideBResponse = championResponse, challengerResponse
	} else {
		sideAResponse, sideBResponse = challengerResponse, championResponse
	}

	packet := PairwiseReviewPacket{
		Kind:            PairwiseReviewPacketKind,
		ContractVersion: ContractVersion,
		Mode:            PairwiseMode,
		TemplateID:      "planner",
		BaseVersionID:   "planner@v1",
		RunID:           "run-1",
		Items: []PairwisePacketItem{
			{
				ItemID: "item-1",
				Title:  "Item One",
				Prompt: "Plan the migration.",
				Outputs: []PairwisePacketSide{
					{Label: "A", Response: sideAResponse},
					{Label: "B", Response: sideBResponse},
				},
			},
		},
	}
	secret := PairwiseSecretMap{
		Kind:            PairwiseSecretMapKind,
		ContractVersion: ContractVersion,
		RunID:           "run-1",
		TemplateID:      "planner",
		ChampionRole:    pairwiseRolePromoted,
		ChallengerRole:  pairwiseRoleCandidate,
		Items: []PairwiseSecretItem{
			{
				ItemID:          "item-1",
				ChampionLabel:   championLabel,
				ChallengerLabel: challengerLabel,
				Mapping: map[string]string{
					championLabel:   pairwiseRolePromoted,
					challengerLabel: pairwiseRoleCandidate,
				},
			},
		},
	}
	picks := PairwisePicks{Picks: []PairwisePick{{ItemID: "item-1", Pick: pick}}}
	return packet, secret, picks
}

// TestUnblindPairwisePacketOrientations is the core correctness test. It pins the
// unblind for BOTH orientations and would FAIL if the secret-map mapping were
// inverted: when A is the champion, a pick of A must resolve to champion; when A
// is the challenger, a pick of A must resolve to challenger. The response routing
// is asserted too so a side-swap is caught.
func TestUnblindPairwisePacketOrientations(t *testing.T) {
	cases := []struct {
		name        string
		championIsA bool
		pick        string
		wantWinner  string
		wantLoser   string
	}{
		{name: "A is champion, pick A -> champion", championIsA: true, pick: "A", wantWinner: PairwiseChampionRole, wantLoser: PairwiseChallengerRole},
		{name: "A is champion, pick B -> challenger", championIsA: true, pick: "B", wantWinner: PairwiseChallengerRole, wantLoser: PairwiseChampionRole},
		{name: "A is challenger, pick A -> challenger", championIsA: false, pick: "A", wantWinner: PairwiseChallengerRole, wantLoser: PairwiseChampionRole},
		{name: "A is challenger, pick B -> champion", championIsA: false, pick: "B", wantWinner: PairwiseChampionRole, wantLoser: PairwiseChallengerRole},
		{name: "lowercase pick is normalized", championIsA: true, pick: "a", wantWinner: PairwiseChampionRole, wantLoser: PairwiseChallengerRole},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			packet, secret, picks := pairwisePacketFixture(tc.championIsA, tc.pick)
			results := UnblindPairwisePacket(packet, secret, picks)
			if len(results) != 1 {
				t.Fatalf("results = %d, want 1", len(results))
			}
			r := results[0]
			if r.Err != nil {
				t.Fatalf("unexpected per-item error: %v", r.Err)
			}
			if r.WinnerLabel != tc.wantWinner {
				t.Fatalf("winner = %q, want %q", r.WinnerLabel, tc.wantWinner)
			}
			if r.LoserLabel != tc.wantLoser {
				t.Fatalf("loser = %q, want %q", r.LoserLabel, tc.wantLoser)
			}
			// The champion option must always carry the champion response and the
			// challenger option the challenger response, regardless of A/B placement.
			if r.ChampionResponse != "CHAMPION-RESPONSE" {
				t.Fatalf("champion response = %q, want CHAMPION-RESPONSE", r.ChampionResponse)
			}
			if r.ChallengerResponse != "CHALLENGER-RESPONSE" {
				t.Fatalf("challenger response = %q, want CHALLENGER-RESPONSE", r.ChallengerResponse)
			}
		})
	}
}

// TestUnblindPairwisePacketDetectsInvertedSecretMap proves the unblind comes
// SOLELY from the secret map: if we feed an INVERTED secret map (mapping says A is
// the candidate while the labels say A is the champion), the unblind must refuse
// rather than silently pick a side — the single scariest bug (a backwards
// preference fed to the optimizer). This test fails if the mapping/label
// cross-check is removed.
func TestUnblindPairwisePacketDetectsInvertedSecretMap(t *testing.T) {
	packet, secret, picks := pairwisePacketFixture(true, "A")
	// Corrupt only the mapping so it disagrees with champion_label/challenger_label.
	secret.Items[0].Mapping = map[string]string{
		"A": pairwiseRoleCandidate, // labels say A is champion; mapping says candidate
		"B": pairwiseRolePromoted,
	}
	results := UnblindPairwisePacket(packet, secret, picks)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Err == nil {
		t.Fatalf("expected an error for an inverted secret map, got winner=%q", results[0].WinnerLabel)
	}
	if !strings.Contains(results[0].Err.Error(), "disagree") {
		t.Fatalf("error = %v, want a mapping/label disagreement", results[0].Err)
	}
}

// TestUnblindPairwisePacketPerItemFailures asserts a missing secret entry, a
// missing pick, and a missing output are each reported PER ITEM without aborting
// the good items.
func TestUnblindPairwisePacketPerItemFailures(t *testing.T) {
	packet := PairwiseReviewPacket{
		Kind:            PairwiseReviewPacketKind,
		ContractVersion: ContractVersion,
		Mode:            PairwiseMode,
		TemplateID:      "planner",
		RunID:           "run-1",
		Items: []PairwisePacketItem{
			{ItemID: "good", Outputs: []PairwisePacketSide{{Label: "A", Response: "champ"}, {Label: "B", Response: "chal"}}},
			{ItemID: "no-secret", Outputs: []PairwisePacketSide{{Label: "A", Response: "x"}, {Label: "B", Response: "y"}}},
			{ItemID: "no-pick", Outputs: []PairwisePacketSide{{Label: "A", Response: "x"}, {Label: "B", Response: "y"}}},
			{ItemID: "missing-output", Outputs: []PairwisePacketSide{{Label: "A", Response: "x"}}},
		},
	}
	secret := PairwiseSecretMap{
		Kind:            PairwiseSecretMapKind,
		ContractVersion: ContractVersion,
		RunID:           "run-1",
		Items: []PairwiseSecretItem{
			{ItemID: "good", ChampionLabel: "A", ChallengerLabel: "B", Mapping: map[string]string{"A": pairwiseRolePromoted, "B": pairwiseRoleCandidate}},
			{ItemID: "no-pick", ChampionLabel: "A", ChallengerLabel: "B", Mapping: map[string]string{"A": pairwiseRolePromoted, "B": pairwiseRoleCandidate}},
			{ItemID: "missing-output", ChampionLabel: "B", ChallengerLabel: "A", Mapping: map[string]string{"B": pairwiseRolePromoted, "A": pairwiseRoleCandidate}},
		},
	}
	picks := PairwisePicks{Picks: []PairwisePick{
		{ItemID: "good", Pick: "A"},
		{ItemID: "no-secret", Pick: "A"},
		{ItemID: "missing-output", Pick: "B"},
	}}

	results := UnblindPairwisePacket(packet, secret, picks)
	if len(results) != 4 {
		t.Fatalf("results = %d, want 4 (one per packet item)", len(results))
	}
	byID := map[string]PairwiseUnblindResult{}
	for _, r := range results {
		byID[r.ItemID] = r
	}
	if r := byID["good"]; r.Err != nil || r.WinnerLabel != PairwiseChampionRole {
		t.Fatalf("good item: err=%v winner=%q", r.Err, r.WinnerLabel)
	}
	if byID["no-secret"].Err == nil {
		t.Fatalf("no-secret item should have errored")
	}
	if byID["no-pick"].Err == nil {
		t.Fatalf("no-pick item should have errored")
	}
	if byID["missing-output"].Err == nil {
		t.Fatalf("missing-output item should have errored")
	}
}

// TestParsePairwisePicksShapes covers both the array and object map picks shapes.
func TestParsePairwisePicksShapes(t *testing.T) {
	arr := []byte(`{"kind":"gitmoot-skillopt-pairwise-picks","run_id":"run-1","picks":[{"item_id":"a","pick":"A"},{"item_id":"b","pick":"B"}]}`)
	got, err := ParsePairwisePicks(arr)
	if err != nil {
		t.Fatalf("array picks: %v", err)
	}
	if len(got.Picks) != 2 {
		t.Fatalf("array picks len = %d, want 2", len(got.Picks))
	}

	obj := []byte(`{"picks":{"a":"A","b":"B"}}`)
	got, err = ParsePairwisePicks(obj)
	if err != nil {
		t.Fatalf("map picks: %v", err)
	}
	if len(got.Picks) != 2 {
		t.Fatalf("map picks len = %d, want 2", len(got.Picks))
	}
}

// TestParsePairwiseReviewPacketRejectsWrongContract guards the additive
// contract: a foreign kind or a non-1 contract_version is rejected.
func TestParsePairwiseReviewPacketRejectsWrongContract(t *testing.T) {
	bad := PairwiseReviewPacket{Kind: "something-else", ContractVersion: ContractVersion, RunID: "r", Items: []PairwisePacketItem{{ItemID: "x"}}}
	data, _ := json.Marshal(bad)
	if _, err := ParsePairwiseReviewPacket(data); err == nil {
		t.Fatalf("expected a kind mismatch error")
	}
	badVersion := PairwiseReviewPacket{Kind: PairwiseReviewPacketKind, ContractVersion: 2, RunID: "r", Items: []PairwisePacketItem{{ItemID: "x"}}}
	data, _ = json.Marshal(badVersion)
	if _, err := ParsePairwiseReviewPacket(data); err == nil {
		t.Fatalf("expected a contract_version mismatch error")
	}
}
