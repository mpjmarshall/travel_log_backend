// The statement splitter, tested without a database.
//
// WHY THE RUNNER SPLITS AT ALL, rather than handing a whole file to one Exec.
// `-- migrate:no-transaction` exists so `CREATE INDEX CONCURRENTLY` can run,
// and the simple query protocol wraps MULTIPLE statements sent in one message
// in an IMPLICIT transaction block — which is the exact thing CONCURRENTLY
// refuses. A file executed statement by statement is not wrapped. Splitting is
// therefore load-bearing for the escape hatch, and once it exists the
// transactional path uses it too, so the splitter is exercised by the real
// migration on every run and an error names the statement that failed.
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

// NO MUTATION OF THE DOUBLED-QUOTE BRANCH CAN REDDEN THIS, and the reason is
// arithmetic rather than accidental: over WELL-FORMED input the naive pairing
// (1,2)(3,4)… and the escape-aware pairing (open at 1, ” at 2-3, close at 4)
// consume exactly the same quotes and put exactly the same characters inside a
// string. Disabling the branch makes the lexer see two adjacent strings instead
// of one, and the semicolon is inside the second either way — measured: the
// mutation ran, changed the file, and the suite stayed green.
//
// The leg is still reddened, by the mutation that stops single quotes opening a
// string at all, so it is coverage rather than decoration. The branch stays
// because the lexer's state should be honest about what it is reading.
func TestSplitStatementsHandlesADoubledQuoteInsideAString(t *testing.T) {
	got := splitStatements("SELECT 'it''s; fine'; SELECT 2;")
	assertStatements(t, got, []string{"SELECT 'it''s; fine'", "SELECT 2"})
}

// E'\” is one string in Postgres. '\' is a COMPLETE string holding a
// backslash — standard_conforming_strings has been on by default since 9.1 —
// so the ';' after it is a real separator.
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

// The shape the escape hatch actually has to survive: the CONCURRENTLY
// statement must come out on its own and byte-clean, because it is executed by
// itself precisely so PostgreSQL does not wrap it in an implicit transaction.
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
