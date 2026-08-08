package search

import (
	"reflect"
	"testing"

	"github.com/tseiman/SecretTUIVault/internal/vault"
)

var entries = []vault.Entry{
	{ID: "1", Name: "Database Login", Description: "production linux host", Tags: []string{"Windows", "Ops"}},
	{ID: "2", Name: "Father Recovery", Description: "codes for Vater", Tags: []string{"Vater", "recovery-codes"}},
	{ID: "3", Name: "Windows Login Database Server", Description: "staging", Tags: []string{"Vater", "Windows"}},
	{ID: "4", Name: "Überblick", Description: "Grüße aus München", Tags: []string{"Persönlich"}},
}

func TestQueryGrammarAndMatching(t *testing.T) {
	cases := []struct {
		query string
		ids   []string
	}{
		{"login", []string{"1", "3"}},
		{"TAG:vater", []string{"2", "3"}},
		{"NAME:\"Login Database\"", []string{"3"}},
		{"TAG:Ops TAG:recovery", []string{"1", "2"}},
		{"TAG:Vater AND TAG:Windows", []string{"3"}},
		{"TAG:Ops AND linux TAG:Vater AND NAME:server", []string{"1", "3"}},
		{"grü mün", []string{"4"}},
		{"", []string{"1", "2", "3", "4"}},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			q, err := Parse(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			gotResults := q.Filter(entries)
			got := make([]string, len(gotResults))
			for i := range gotResults {
				got[i] = gotResults[i].Entry.ID
			}
			if !reflect.DeepEqual(got, tc.ids) {
				t.Fatalf("got %v want %v", got, tc.ids)
			}
		})
	}
}

func TestQuotedANDIsSearchTextNotAnOperator(t *testing.T) {
	entry := vault.Entry{ID: "and", Name: "AND notes", Description: "logical operator"}
	for _, input := range []string{`"AND"`, `NAME:"AND"`} {
		query, err := Parse(input)
		if err != nil {
			t.Fatalf("Parse(%q): %v", input, err)
		}
		results := query.Filter([]vault.Entry{entry})
		if len(results) != 1 || results[0].Entry.ID != "and" {
			t.Fatalf("quoted AND did not match for %q: %#v", input, results)
		}
	}
}

func TestQueryRejectsMalformedInput(t *testing.T) {
	for _, input := range []string{`"unterminated`, `TAG:"unterminated`, `AND foo`, `foo AND`, `foo AND AND bar`, `TAG:`} {
		t.Run(input, func(t *testing.T) {
			if _, err := Parse(input); err == nil {
				t.Fatal("expected parse error")
			}
		})
	}
}

func TestRankingIsStableAndFavorsCloserMatches(t *testing.T) {
	q, err := Parse("database")
	if err != nil {
		t.Fatal(err)
	}
	got := q.Filter([]vault.Entry{{ID: "z", Name: "xxdatabasexx"}, {ID: "a", Name: "Database"}, {ID: "b", Name: "Database"}})
	ids := []string{got[0].Entry.ID, got[1].Entry.ID, got[2].Entry.ID}
	if !reflect.DeepEqual(ids, []string{"a", "b", "z"}) {
		t.Fatalf("order %v", ids)
	}
}

func FuzzParseNeverPanics(f *testing.F) {
	for _, seed := range []string{"", "foo AND bar", `NAME:"hello world"`, "TAG:Übung"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		q, err := Parse(input)
		if err == nil {
			_ = q.Filter(entries)
		}
	})
}
