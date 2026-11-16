package autonomy

import (
	"context"
	"fmt"
	"openchris/conselho/internal/memory"
	"testing"
	"time"
)

func TestArchiveStablePaginationAndOwnerScope(t *testing.T) {
	s, store, _, _ := fixture(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 45; i++ {
		id := fmt.Sprintf("11111111-1111-4111-8111-%012d", i)
		_, err := store.DB.Exec(ctx, `INSERT INTO autonomy_runs(id,user_id,status,reason,topic,fingerprint,created_at) VALUES($1,$2,'complete','fixture','Arquivo','fixture',$3)`, id, memory.UserID, at)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := store.DB.Exec(ctx, `INSERT INTO autonomy_runs(id,user_id,status,reason,topic,fingerprint,created_at) VALUES($1,$2,'complete','fixture','Outro proprietário','fixture',$3)`, memory.ID(), memory.ID(), at)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		page, err := s.Archive(ctx, cursor)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, r := range page.Runs {
			if seen[r.ID] || r.Topic != "Arquivo" {
				t.Fatalf("duplicate or foreign row: %+v", r)
			}
			seen[r.ID] = true
		}
		if page.Next == "" {
			break
		}
		cursor = page.Next
		if pages > 4 {
			t.Fatal("pagination did not end")
		}
	}
	if len(seen) != 45 || pages != 3 {
		t.Fatalf("archive lost rows: %d in %d pages", len(seen), pages)
	}
	if _, err := s.Archive(ctx, "bad"); err != ErrArchiveCursor {
		t.Fatalf("invalid cursor: %v", err)
	}
}
