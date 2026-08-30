// What the runner will and will not accept as a migration directory, tested
// without a database.
package postgres

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"testing/fstest"
)

func pair(up, down string) fstest.MapFS {
	return fstest.MapFS{
		up:   &fstest.MapFile{Data: []byte("SELECT 1;\n")},
		down: &fstest.MapFile{Data: []byte("SELECT 2;\n")},
	}
}

func TestLoadMigrationsOrdersLexicallyAndCarriesEveryVersion(t *testing.T) {
	fsys := fstest.MapFS{}
	for _, n := range []string{"0010_j", "0002_b", "0001_a"} {
		for name, file := range pair(n+".up.sql", n+".down.sql") {
			fsys[name] = file
		}
	}
	got, err := loadMigrations(fsys)
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	want := []string{"0001", "0002", "0010"}
	if len(got) != len(want) {
		t.Fatalf("loadMigrations returned %d migrations, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Version != want[i] {
			t.Errorf("migration %d version = %q, want %q", i, got[i].Version, want[i])
		}
	}
}

// "2" sorts after "10", which is the whole reason the rule exists.
func TestLoadMigrationsRefusesAnUnpaddedVersion(t *testing.T) {
	fsys := pair("2_second.up.sql", "2_second.down.sql")
	_, err := loadMigrations(fsys)
	if err == nil {
		t.Fatalf("loadMigrations accepted %q; lexical order over embed.FS puts 10 before 2", "2_second.up.sql")
	}
	if !strings.Contains(err.Error(), "2_second") {
		t.Errorf("error does not name the offending file: %v", err)
	}
}

func TestLoadMigrationsRefusesANameOutsideTheAlphabet(t *testing.T) {
	for _, name := range []string{"0001_Init.up.sql", "0001_init-two.up.sql", "0001 init.up.sql", "0001_init.sql"} {
		fsys := fstest.MapFS{
			name:                  &fstest.MapFile{Data: []byte("SELECT 1;\n")},
			"0001_init.down.sql":  &fstest.MapFile{Data: []byte("SELECT 2;\n")},
			"0001_other.down.sql": &fstest.MapFile{Data: []byte("SELECT 2;\n")},
		}
		if _, err := loadMigrations(fsys); err == nil {
			t.Errorf("loadMigrations accepted %q, want a refusal", name)
		}
	}
}

func TestLoadMigrationsRefusesTwoFilesAtOneVersion(t *testing.T) {
	fsys := fstest.MapFS{
		"0001_a.up.sql":   &fstest.MapFile{Data: []byte("SELECT 1;\n")},
		"0001_a.down.sql": &fstest.MapFile{Data: []byte("SELECT 2;\n")},
		"0001_b.up.sql":   &fstest.MapFile{Data: []byte("SELECT 1;\n")},
		"0001_b.down.sql": &fstest.MapFile{Data: []byte("SELECT 2;\n")},
	}
	_, err := loadMigrations(fsys)
	if err == nil {
		t.Fatalf("loadMigrations accepted two files at version 0001")
	}
	if !strings.Contains(err.Error(), "0001") {
		t.Errorf("error does not name the duplicated version: %v", err)
	}
}

func TestLoadMigrationsRefusesAnUpFileWithNoDownFile(t *testing.T) {
	fsys := fstest.MapFS{"0001_a.up.sql": &fstest.MapFile{Data: []byte("SELECT 1;\n")}}
	_, err := loadMigrations(fsys)
	if err == nil {
		t.Fatalf("loadMigrations accepted an .up.sql with no .down.sql")
	}
	if !strings.Contains(err.Error(), "0001_a.down.sql") {
		t.Errorf("error does not name the missing file: %v", err)
	}
}

func TestLoadMigrationsChecksumsTheFileBytes(t *testing.T) {
	body := "-- header\nCREATE TABLE t (x int);\n"
	fsys := fstest.MapFS{
		"0001_a.up.sql":   &fstest.MapFile{Data: []byte(body)},
		"0001_a.down.sql": &fstest.MapFile{Data: []byte("DROP TABLE t;\n")},
	}
	got, err := loadMigrations(fsys)
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	sum := sha256.Sum256([]byte(body))
	if want := hex.EncodeToString(sum[:]); got[0].Checksum != want {
		t.Errorf("checksum = %q, want the sha256 of the file bytes %q", got[0].Checksum, want)
	}
}

func TestLoadMigrationsReadsTheNoTransactionDirectiveOnTheFirstLineOnly(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"first line", "-- migrate:no-transaction\n-- migrate:re-runnable\nCREATE INDEX CONCURRENTLY IF NOT EXISTS i ON t (c);\n", true},
		{"absent", "CREATE TABLE t (x int);\n", false},
		{"second line", "-- a header\n-- migrate:no-transaction\nCREATE TABLE t (x int);\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"0001_a.up.sql":   &fstest.MapFile{Data: []byte(c.body)},
				"0001_a.down.sql": &fstest.MapFile{Data: []byte("SELECT 1;\n")},
			}
			got, err := loadMigrations(fsys)
			if err != nil {
				t.Fatalf("loadMigrations: %v", err)
			}
			if got[0].NoTransaction != c.want {
				t.Errorf("NoTransaction = %v, want %v for %q", got[0].NoTransaction, c.want, c.body)
			}
		})
	}
}

func TestLoadMigrationsRefusesAnEmptyDirectory(t *testing.T) {
	if _, err := loadMigrations(fstest.MapFS{}); err == nil {
		t.Fatalf("loadMigrations accepted a directory with no migrations in it")
	}
}
