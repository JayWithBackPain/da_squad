package sqlloader_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jay/da-agents/internal/sqlloader"
)

func TestLoadDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "b.sql"), []byte("SELECT 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.sql"), []byte("SELECT 2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	qs, err := sqlloader.LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 2 || qs[0].Name != "a" || qs[1].Name != "b" {
		t.Fatalf("unexpected %#v", qs)
	}
}

func TestParseMeta(t *testing.T) {
	raw := `-- @desc: 付費用戶消費分布（人均／中位／P90）
-- @role: supporting
-- @supports: revenue_daily, arpu
SELECT 1 AS x;
`
	meta, sql := sqlloader.ParseMeta(raw)
	if meta.Description == "" || meta.Role != "supporting" || len(meta.Supports) != 2 {
		t.Fatalf("meta=%#v", meta)
	}
	if !strings.HasPrefix(strings.TrimSpace(sql), "SELECT") {
		t.Fatalf("sql=%q", sql)
	}
}
