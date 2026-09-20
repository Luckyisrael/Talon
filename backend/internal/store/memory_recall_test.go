package store

import "testing"

// world-standard recall eval: FTS must recall MWA via wallet, not just substring
func TestMemoryRecallFTS(t *testing.T) {
	s := newTestStore(t)
	// seed user + project
	if _, err := s.db.Exec("INSERT OR IGNORE INTO users (id, email, password_hash) VALUES (1, 'a@a', 'x')"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := s.db.Exec("INSERT OR IGNORE INTO projects (id, user_id, name, slug) VALUES (1, 1, 'p1', 'p1')"); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	// semantic facts
	_ = s.AddMemory(1, "fact", "MWA adapter via @wallet-ui/react-native-kit sendTransactions", "")
	_ = s.AddMemory(1, "fact", "uses expo-router file routing", "")
	_ = s.AddMemory(1, "fact", "solana kit Address strings", "")

	// substring "wallet" should hit MWA via FTS porter stemming (wallets -> wallet) and token match
	rows, err := s.SearchMemoryFTS(1, "wallet", "", 10)
	if err != nil {
		t.Fatalf("fts: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.Content == "MWA adapter via @wallet-ui/react-native-kit sendTransactions" {
			found = true
		}
	}
	if !found {
		t.Fatalf("FTS recall failed: wallet should recall MWA, got %+v", rows)
	}
	// unknown term should yield empty, not error
	rows, err = s.SearchMemoryFTS(1, "unicornzz", "", 10)
	if err != nil {
		t.Fatalf("fts unicorn: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("unknown term should yield 0, got %d", len(rows))
	}
}
