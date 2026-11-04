package main

import "testing"

func TestExtractDB(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantDB  string
		wantHas []string
		wantErr bool
	}{
		{name: "long form", args: []string{"config", "get", "--db", `D:\land.db`}, wantDB: `D:\land.db`, wantHas: []string{"config", "get"}},
		{name: "equals form", args: []string{"--db=D:/land.db", "version"}, wantDB: "D:/land.db", wantHas: []string{"version"}},
		{name: "absent", args: []string{"config", "get"}, wantHas: []string{"config", "get"}},
		{name: "trailing without value", args: []string{"config", "set", "port", "8080", "--db"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, rest, err := extractDB(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got db=%q rest=%v", db, rest)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if db != tc.wantDB {
				t.Fatalf("db = %q, want %q", db, tc.wantDB)
			}
			if len(rest) != len(tc.wantHas) {
				t.Fatalf("rest = %v, want %v", rest, tc.wantHas)
			}
			for i := range rest {
				if rest[i] != tc.wantHas[i] {
					t.Fatalf("rest = %v, want %v", rest, tc.wantHas)
				}
			}
		})
	}
}
