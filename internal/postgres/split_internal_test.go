// The statement splitter, tested without a database.
package postgres

import "testing"

func TestSplitStatementsOnPlainSemicolons(t *testing.T) {
	got := splitStatements("SELECT 1; SELECT 2;")
	want := []string{"SELECT 1", "SELECT 2"}
	assertStatements(t, got, want)
}

func TestSplitStatementsKeepsATrailingStatementWithNoSemicolon(t *testing.T) {
	got := splitStatements("SELECT 1;\nSELECT 2\n")
	assertStatements(t, got, []string{"SELECT 1", "SELECT 2"})
}

func TestSplitStatementsDropsEmptyChunks(t *testing.T) {
	got := splitStatements(";;\n SELECT 1;;\n\n;")
	assertStatements(t, got, []string{"SELECT 1"})
}

func TestSplitStatementsDropsAChunkThatIsOnlyAComment(t *testing.T) {
	got := splitStatements("-- a header\n/* and a block */\nSELECT 1;\n-- a footer\n")
	assertStatements(t, got, []string{"-- a header\n/* and a block */\nSELECT 1"})
}

func TestSplitStatementsIgnoresASemicolonInsideASingleQuotedString(t *testing.T) {
	got := splitStatements("INSERT INTO t VALUES ('a;b'); SELECT 2;")
	assertStatements(t, got, []string{"INSERT INTO t VALUES ('a;b')", "SELECT 2"})
}

// no mutation of the doubled-quote branch can redden this, and the reason is
// arithmetic rather than accidental.
func TestSplitStatementsHandlesADoubledQuoteInsideAString(t *testing.T) {
	got := splitStatements("SELECT 'it''s; fine'; SELECT 2;")
	assertStatements(t, got, []string{"SELECT 'it''s; fine'", "SELECT 2"})
}

// E'\” is one string in Postgres.
func TestSplitStatementsHandlesABackslashEscapeOnlyInsideAnEString(t *testing.T) {
	got := splitStatements(`SELECT E'\';'; SELECT 2;`)
	assertStatements(t, got, []string{`SELECT E'\';'`, "SELECT 2"})

	got = splitStatements(`SELECT '\'; SELECT 2;`)
	assertStatements(t, got, []string{`SELECT '\'`, "SELECT 2"})
}

func TestSplitStatementsIgnoresASemicolonInsideAQuotedIdentifier(t *testing.T) {
	got := splitStatements(`CREATE TABLE "we;ird" (x int); SELECT 2;`)
	assertStatements(t, got, []string{`CREATE TABLE "we;ird" (x int)`, "SELECT 2"})
}

func TestSplitStatementsIgnoresASemicolonInsideADollarQuotedBody(t *testing.T) {
	src := "CREATE FUNCTION f() RETURNS int AS $$ BEGIN; RETURN 1; END; $$ LANGUAGE plpgsql; SELECT 2;"
	got := splitStatements(src)
	assertStatements(t, got, []string{
		"CREATE FUNCTION f() RETURNS int AS $$ BEGIN; RETURN 1; END; $$ LANGUAGE plpgsql",
		"SELECT 2",
	})
}

func TestSplitStatementsHandlesATaggedDollarQuote(t *testing.T) {
	src := "SELECT $tag$ a; $notatag$ b; $tag$; SELECT 2;"
	got := splitStatements(src)
	assertStatements(t, got, []string{"SELECT $tag$ a; $notatag$ b; $tag$", "SELECT 2"})
}

func TestSplitStatementsDoesNotMistakeAPositionalParameterForADollarQuote(t *testing.T) {
	got := splitStatements("SELECT $1; SELECT 2;")
	assertStatements(t, got, []string{"SELECT $1", "SELECT 2"})
}

func TestSplitStatementsIgnoresASemicolonInALineComment(t *testing.T) {
	got := splitStatements("SELECT 1 -- ; not a separator\n; SELECT 2;")
	assertStatements(t, got, []string{"SELECT 1 -- ; not a separator", "SELECT 2"})
}

// PostgreSQL block comments NEST, unlike C's.
func TestSplitStatementsIgnoresASemicolonInANestedBlockComment(t *testing.T) {
	got := splitStatements("SELECT 1 /* outer /* inner ; */ still ; */ ; SELECT 2;")
	assertStatements(t, got, []string{"SELECT 1 /* outer /* inner ; */ still ; */", "SELECT 2"})
}

// The shape the escape hatch actually has to survive.
func TestTheConcurrentlyStatementComesOutOnItsOwnAndUntouched(t *testing.T) {
	got := splitStatements("CREATE TABLE one (x int);\nCREATE INDEX CONCURRENTLY one_x_idx ON one (x);\n")
	assertStatements(t, got, []string{
		"CREATE TABLE one (x int)",
		"CREATE INDEX CONCURRENTLY one_x_idx ON one (x)",
	})
}

func assertStatements(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("splitStatements = %d statements %q, want %d %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("statement %d = %q, want %q", i, got[i], want[i])
		}
	}
}
