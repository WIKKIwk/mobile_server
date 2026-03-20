package core

import "testing"

func TestPushTokenStoreMoveTokenToKeyPreservesOtherTargetTokens(t *testing.T) {
	store := NewPushTokenStore(t.TempDir() + "/push_tokens.json")
	if err := store.Put("supplier:SUP-001", "device-a", "android"); err != nil {
		t.Fatalf("seed first target token: %v", err)
	}
	if err := store.Put("supplier:SUP-001", "device-b", "android"); err != nil {
		t.Fatalf("seed second target token: %v", err)
	}

	if err := store.MoveTokenToKey("supplier:SUP-001", "device-c", "android"); err != nil {
		t.Fatalf("MoveTokenToKey() error = %v", err)
	}

	got, err := store.List("supplier:SUP-001")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 tokens after move, got %d: %+v", len(got), got)
	}
	assertHasPushToken(t, got, "device-a")
	assertHasPushToken(t, got, "device-b")
	assertHasPushToken(t, got, "device-c")
}

func TestPushTokenStoreMoveTokenToKeyRemovesFromPreviousOwnerOnly(t *testing.T) {
	store := NewPushTokenStore(t.TempDir() + "/push_tokens.json")
	if err := store.Put("supplier:SUP-001", "shared-device", "android"); err != nil {
		t.Fatalf("seed previous owner token: %v", err)
	}
	if err := store.Put("werka:werka", "werka-device", "android"); err != nil {
		t.Fatalf("seed existing target token: %v", err)
	}

	if err := store.MoveTokenToKey("werka:werka", "shared-device", "android"); err != nil {
		t.Fatalf("MoveTokenToKey() error = %v", err)
	}

	previousOwner, err := store.List("supplier:SUP-001")
	if err != nil {
		t.Fatalf("List(previous owner) error = %v", err)
	}
	if len(previousOwner) != 0 {
		t.Fatalf("expected previous owner token to be removed, got %+v", previousOwner)
	}

	targetOwner, err := store.List("werka:werka")
	if err != nil {
		t.Fatalf("List(target owner) error = %v", err)
	}
	if len(targetOwner) != 2 {
		t.Fatalf("expected target owner to keep both tokens, got %d: %+v", len(targetOwner), targetOwner)
	}
	assertHasPushToken(t, targetOwner, "werka-device")
	assertHasPushToken(t, targetOwner, "shared-device")
}

func assertHasPushToken(t *testing.T, records []PushTokenRecord, token string) {
	t.Helper()
	for _, item := range records {
		if item.Token == token {
			return
		}
	}
	t.Fatalf("expected token %q in %+v", token, records)
}
