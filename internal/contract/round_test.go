package contract

import "testing"

// A link stands above a protected path where its own path is how a pattern
// begins; and a pattern that can match nothing is not taken for the list.
func TestALinkAboveAndAPatternOfNothing(t *testing.T) {
	list := []string{"deploy/**", ".github/workflows/**", "db/*/migrations", ".env*"}
	for file, want := range map[string]bool{".github": true, "db": true, "db/x": true, "deploy": false, "db/x/migrations": false,
		".github/workflows/a": false, "src": false, "src/.github": false, ".envrc": false} {
		if got := Above(list, file); got != want {
			t.Errorf("a link at %s above the list: %v", file, got)
		}
	}
	if !Above([]string{"**/secrets/**"}, "any/link") || Above([]string{"**"}, "") == false {
		t.Error("with two stars in front any link is above")
	}
	key, _ := FindKey("protect.paths")
	for value, ok := range map[string]bool{"db/migrations/**,.env*": true, "a[bc]/x": true, "db/[migrations/**": false, `deploy\`: false, "ok,[": false} {
		if _, err := key.Check(value, nil); (err == nil) != ok {
			t.Errorf("protect.paths %q: %v", value, err)
		}
	}
}
