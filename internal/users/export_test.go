package users

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestExportData_ContainsMyDataAndNothingAboutOthersOrSecrets(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc := NewService(NewRepository(pool))
	sfx := time.Now().Format("150405.000000000")
	var me, other string
	pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, "export-"+sfx+"@example.com").Scan(&me)
	pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, "export-other-"+sfx+"@example.com").Scan(&other)
	for _, q := range []struct {
		sql string
		arg string
	}{
		{`INSERT INTO user_profiles (user_id, display_name, bio) VALUES ($1,'Asha','export bio')`, me},
		{`INSERT INTO posts (author_id, body, visibility) VALUES ($1,'my export post','public')`, me},
		{`INSERT INTO posts (author_id, body, visibility) VALUES ($1,'someone elses post','public')`, other},
		{`INSERT INTO devices (user_id, device_id, push_token, platform) VALUES ($1,'d1','SECRET-PUSH-TOKEN','android')`, me},
	} {
		if _, err := pool.Exec(ctx, q.sql, q.arg); err != nil {
			t.Fatal(err)
		}
	}

	raw, err := svc.ExportData(ctx, me)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string][]map[string]any
	delete0 := func(m map[string]json.RawMessage) { delete(m, "exported_at") }
	var sections map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sections); err != nil {
		t.Fatal(err)
	}
	delete0(sections)
	b, _ := json.Marshal(sections)
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got["account"]) != 1 || got["account"][0]["email"] != "export-"+sfx+"@example.com" {
		t.Fatalf("account section wrong: %v", got["account"])
	}
	if len(got["profile"]) != 1 || len(got["posts"]) != 1 || len(got["devices"]) != 1 {
		t.Fatalf("expected my profile, my post and my device: %v", got)
	}
	s := string(raw)
	if strings.Contains(s, "someone elses post") || strings.Contains(s, "SECRET-PUSH-TOKEN") {
		t.Fatal("the export leaked another person's data or a push token")
	}
}
