package parser

import (
	"testing"
)

type scanResult struct {
	Token         int
	Literal       string
	Quoted        bool
	HolderOrdinal int
	Line          int
	Char          int
}

var scanTests = []struct {
	Name        string
	Input       string
	DTFormats   []string
	ForPrepared bool
	Output      []scanResult
	Error       string
}{
	{
		Name:  "Identifier",
		Input: "identifier",
		Output: []scanResult{
			{
				Token:   IDENTIFIER,
				Literal: "identifier",
			},
		},
	},
	{
		Name:  "QuotedIdentifier",
		Input: "`id\\enti\\`fier`",
		Output: []scanResult{
			{
				Token:   IDENTIFIER,
				Literal: "id\\enti`fier",
				Quoted:  true,
			},
		},
	},
	{
		Name:  "QuotedString",
		Input: "\"string\\\"\"",
		Output: []scanResult{
			{
				Token:   STRING,
				Literal: "string\"",
			},
		},
	},
	{
		Name:  "QuotedString 2",
		Input: "\"string\\\\\"",
		Output: []scanResult{
			{
				Token:   STRING,
				Literal: "string\\",
			},
		},
	},
	{
		Name:  "QuotedString(Single-Quote)",
		Input: "'strin\\'g string'",
		Output: []scanResult{
			{
				Token:   STRING,
				Literal: "strin'g string",
			},
		},
	},
	{
		Name:  "Integer",
		Input: "1",
		Output: []scanResult{
			{
				Token:   INTEGER,
				Literal: "1",
			},
		},
	},
	{
		Name:  "Float",
		Input: "1.234",
		Output: []scanResult{
			{
				Token:   FLOAT,
				Literal: "1.234",
			},
		},
	},
	{
		Name:  "Ternary",
		Input: "true",
		Output: []scanResult{
			{
				Token:   TERNARY,
				Literal: "true",
			},
		},
	},
	{
		Name:  "Datetime",
		Input: "\"2012-05-21 12:00:00\"",
		Output: []scanResult{
			{
				Token:   DATETIME,
				Literal: "2012-05-21 12:00:00",
			},
		},
	},
	{
		Name:  "Datetime(RFC3339)",
		Input: "\"2012-05-21T12:00:00-12:00\"",
		Output: []scanResult{
			{
				Token:   DATETIME,
				Literal: "2012-05-21T12:00:00-12:00",
			},
		},
	},
	{
		Name:      "Datetime",
		Input:     "\"20120521\"",
		DTFormats: []string{"%Y%m%d"},
		Output: []scanResult{
			{
				Token:   DATETIME,
				Literal: "20120521",
			},
		},
	},
	{
		Name:  "Flag",
		Input: "@@flag",
		Output: []scanResult{
			{
				Token:   FLAG,
				Literal: "flag",
			},
		},
	},
	{
		Name:  "Variable",
		Input: "@var",
		Output: []scanResult{
			{
				Token:   VARIABLE,
				Literal: "var",
			},
		},
	},
	{
		Name:  "Environment Variable",
		Input: "@%var",
		Output: []scanResult{
			{
				Token:   ENVIRONMENT_VARIABLE,
				Literal: "var",
			},
		},
	},
	{
		Name:  "Environment Variable Quoted",
		Input: "@%`var`",
		Output: []scanResult{
			{
				Token:   ENVIRONMENT_VARIABLE,
				Literal: "var",
				Quoted:  true,
			},
		},
	},
	{
		Name:  "Runtime Information",
		Input: "@#var",
		Output: []scanResult{
			{
				Token:   RUNTIME_INFORMATION,
				Literal: "var",
			},
		},
	},
	{
		Name:  "EqualSign",
		Input: "=",
		Output: []scanResult{
			{
				Token:   '=',
				Literal: "=",
			},
		},
	},
	{
		Name:  "ComparisonOperator",
		Input: "<=",
		Output: []scanResult{
			{
				Token:   COMPARISON_OP,
				Literal: "<=",
			},
		},
	},
	{
		Name:  "StringOperator",
		Input: "||",
		Output: []scanResult{
			{
				Token:   STRING_OP,
				Literal: "||",
			},
		},
	},
	{
		Name:  "SubstitutionOperator",
		Input: ":=",
		Output: []scanResult{
			{
				Token:   SUBSTITUTION_OP,
				Literal: ":=",
			},
		},
	},
	{
		Name:  "UncategorizedOperator",
		Input: "====",
		Output: []scanResult{
			{
				Token:   Uncategorized,
				Literal: "====",
			},
		},
	},
	{
		Name:  "Keyword",
		Input: "select",
		Output: []scanResult{
			{
				Token:   SELECT,
				Literal: "select",
			},
		},
	},
	{
		Name:  "AggregateFunction",
		Input: "sum",
		Output: []scanResult{
			{
				Token:   AGGREGATE_FUNCTION,
				Literal: "sum",
			},
		},
	},
	{
		Name:  "AnalyticFunction",
		Input: "rank",
		Output: []scanResult{
			{
				Token:   ANALYTIC_FUNCTION,
				Literal: "rank",
			},
		},
	},
	{
		Name:  "FunctionNTH",
		Input: "nth_value",
		Output: []scanResult{
			{
				Token:   FUNCTION_NTH,
				Literal: "nth_value",
			},
		},
	},
	{
		Name:  "FunctionWithINS",
		Input: "lag",
		Output: []scanResult{
			{
				Token:   FUNCTION_WITH_INS,
				Literal: "lag",
			},
		},
	},
	{
		Name:  "PassThrough",
		Input: ",",
		Output: []scanResult{
			{
				Token:   int(','),
				Literal: ",",
			},
		},
	},
	{
		Name:  "Statement",
		Input: "identifier   'string', \n 1-2",
		Output: []scanResult{
			{
				Token:   IDENTIFIER,
				Literal: "identifier",
			},
			{
				Token:   STRING,
				Literal: "string",
			},
			{
				Token:   int(','),
				Literal: ",",
			},
			{
				Token:   INTEGER,
				Literal: "1",
			},
			{
				Token:   int('-'),
				Literal: "-",
			},
			{
				Token:   INTEGER,
				Literal: "2",
			},
		},
	},
	{
		Name:  "Comment",
		Input: "identifier/* 'string', \n 1*/-2",
		Output: []scanResult{
			{
				Token:   IDENTIFIER,
				Literal: "identifier",
			},
			{
				Token:   int('-'),
				Literal: "-",
			},
			{
				Token:   INTEGER,
				Literal: "2",
			},
		},
	},
	{
		Name:  "CommentNotTerminated",
		Input: "identifier/* 'string', \n 1-2",
		Output: []scanResult{
			{
				Token:   IDENTIFIER,
				Literal: "identifier",
			},
		},
	},
	{
		Name:  "External Command",
		Input: "$abc",
		Output: []scanResult{
			{
				Token:   EXTERNAL_COMMAND,
				Literal: "abc",
			},
		},
	},
	{
		Name:  "External Command with LineBreak",
		Input: "$abc\nd\\ef\n ghi\\",
		Output: []scanResult{
			{
				Token:   EXTERNAL_COMMAND,
				Literal: "abc\nd\\ef\n ghi\\",
			},
		},
	},
	{
		Name:  "External Command with Terminator",
		Input: "$abc 'def;' ${ghi;} @%`var;`;",
		Output: []scanResult{
			{
				Token:   EXTERNAL_COMMAND,
				Literal: "abc 'def;' ${ghi;} @%`var;`",
			},
			{
				Token:   ';',
				Literal: ";",
			},
		},
	},
	{
		Name:  "LineComment",
		Input: "identifier-- comment 'string', \n 1-2 -- comment \r 2 -- comment",
		Output: []scanResult{
			{
				Token:   IDENTIFIER,
				Literal: "identifier",
			},
			{
				Token:   INTEGER,
				Literal: "1",
			},
			{
				Token:   int('-'),
				Literal: "-",
			},
			{
				Token:   INTEGER,
				Literal: "2",
			},
			{
				Token:   INTEGER,
				Literal: "2",
			},
		},
	},
	{
		Name:  "Line and Char Count",
		Input: "a, \n  /* \n\n */ \r\n c \rd 'abc\ndef' --f\n g",
		Output: []scanResult{
			{
				Token:   IDENTIFIER,
				Literal: "a",
				Line:    1,
				Char:    1,
			},
			{
				Token:   int(','),
				Literal: ",",
				Line:    1,
				Char:    2,
			},
			{
				Token:   IDENTIFIER,
				Literal: "c",
				Line:    5,
				Char:    2,
			},
			{
				Token:   IDENTIFIER,
				Literal: "d",
				Line:    6,
				Char:    1,
			},
			{
				Token:   STRING,
				Literal: "abc\ndef",
				Line:    6,
				Char:    3,
			},
			{
				Token:   IDENTIFIER,
				Literal: "g",
				Line:    8,
				Char:    2,
			},
		},
	},
	{
		Name:  "LiteralNotTerminatedError",
		Input: "\"string",
		Error: "literal not terminated",
	},
	{
		Name:  "LiteralNotTerminatedError 2",
		Input: "\"",
		Error: "literal not terminated",
	},
	{
		Name:  "Invalid Variable Symbol",
		Input: "@@@",
		Error: "invalid variable symbol",
	},
	{
		Name:        "Placeholders",
		Input:       "? :foo",
		ForPrepared: true,
		Output: []scanResult{
			{
				Token:         PLACEHOLDER,
				Literal:       "?",
				HolderOrdinal: 1,
			},
			{
				Token:         PLACEHOLDER,
				Literal:       ":foo",
				HolderOrdinal: 2,
			},
		},
	},
	{
		Name:        "Placeholder Disabled",
		Input:       "?",
		ForPrepared: false,
		Output: []scanResult{
			{
				Token:   '?',
				Literal: "?",
			},
		},
	},
	{
		Name:        "Placeholder Disabled",
		Input:       ":foo",
		ForPrepared: false,
		Output: []scanResult{
			{
				Token:   ':',
				Literal: ":",
			},
			{
				Token:   IDENTIFIER,
				Literal: "foo",
			},
		},
	},
}

func TestScanner_Scan(t *testing.T) {
	for _, v := range scanTests {
		s := new(Scanner).Init(v.Input, "", v.DTFormats, v.ForPrepared)

		tokenCount := 0
		for {
			token, err := s.Scan()
			tokenCount++

			if err != nil {
				if v.Error == "" {
					t.Errorf("%s, token %d: unexpected error %q", v.Name, tokenCount, err.Error())
				} else if v.Error != err.Error() {
					t.Errorf("%s, token %d: error %q, want error %q", v.Name, tokenCount, err.Error(), v.Error)
				}
				break
			}
			if v.Error != "" {
				t.Errorf("%s, token %d: no error, want error %q", v.Name, tokenCount, v.Error)
				break
			}

			if token.Token == EOF {
				tokenCount--
				if tokenCount != len(v.Output) {
					t.Errorf("%s: scan %d token(s) in a statement, want %d token(s)", v.Name, tokenCount, len(v.Output))
				}
				break
			}

			if len(v.Output) < tokenCount {
				t.Errorf("%s: scan %d token(s) in a statement, want %d token(s)", v.Name, tokenCount, len(v.Output))
				break
			}
			expect := v.Output[tokenCount-1]
			if token.Token != expect.Token {
				t.Errorf("%s, token %d: token = %s, want %s", v.Name, tokenCount, TokenLiteral(token.Token), TokenLiteral(expect.Token))
			}
			if token.Literal != expect.Literal {
				t.Errorf("%s, token %d: literal = %q, want %q", v.Name, tokenCount, token.Literal, expect.Literal)
			}
			if token.Quoted != expect.Quoted {
				t.Errorf("%s, token %d: quoted = %t, want %t", v.Name, tokenCount, token.Quoted, expect.Quoted)
			}
			if token.HolderOrdinal != expect.HolderOrdinal {
				t.Errorf("%s, token %d: holder ordinal = %d, want %d", v.Name, tokenCount, token.HolderOrdinal, expect.HolderOrdinal)
			}
			if 0 < expect.Line {
				if token.Line != expect.Line {
					t.Errorf("%s, token %d: line %d: want %d", v.Name, tokenCount, token.Line, expect.Line)
				}
				if token.Char != expect.Char {
					t.Errorf("%s, token %d: char %d: want %d", v.Name, tokenCount, token.Char, expect.Char)
				}
			}
		}
	}
}

var tokenLiteralTests = map[int]string{
	SELECT: "SELECT",
	43:     "+",
}

func TestTokenLiteral(t *testing.T) {
	for k, v := range tokenLiteralTests {
		n := TokenLiteral(k)
		if n != v {
			t.Errorf("token literal = %q, want %q for %d", n, v, k)
		}
	}
}
