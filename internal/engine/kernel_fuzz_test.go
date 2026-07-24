package engine

import "testing"

func FuzzApplyDuplicateAndReplay(f *testing.F) {
	f.Add([]byte(`{"command":"first"}`), []byte(`{"command":"second"}`))
	f.Add([]byte{}, []byte{0x00, 0xff})

	f.Fuzz(func(t *testing.T, firstPayload []byte, secondPayload []byte) {
		first := testInput(t, 1)
		first.Payload = append([]byte(nil), firstPayload...)
		second := testInput(t, 2)
		second.Payload = append([]byte(nil), secondPayload...)

		state, firstDecision, err := Apply(NewState(7), first)
		if err != nil {
			t.Fatalf("first Apply: %v", err)
		}
		duplicateState, duplicateDecision, err := Apply(state, first)
		if err != nil {
			t.Fatalf("duplicate Apply: %v", err)
		}
		if duplicateState.Hash() != state.Hash() || duplicateDecision != firstDecision {
			t.Fatalf("duplicate input was not idempotent")
		}

		leftState, leftDecisions := replay(t, []InputEnvelope{first, second})
		rightState, rightDecisions := replay(t, []InputEnvelope{first, second})
		if leftState.Hash() != rightState.Hash() {
			t.Fatalf("replay state hash differs: %s != %s", leftState.Hash(), rightState.Hash())
		}
		for index := range leftDecisions {
			if leftDecisions[index] != rightDecisions[index] {
				t.Fatalf("replay decision %d differs", index)
			}
		}
	})
}
