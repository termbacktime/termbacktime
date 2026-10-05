package sharing

import (
	"strings"
	"testing"
)

func TestReferences(t *testing.T) {
	id := strings.Repeat("a", 32)
	for _, input := range []string{id, "https://gist.github.com/alice/" + id, "https://play.example/p/" + id} {
		r, err := Parse(input)
		if err != nil || r.Storage != Gist || r.ID != id {
			t.Fatal(input, r, err)
		}
	}
	for _, input := range []string{"Alice/" + id, "https://play.example/p/Alice/" + id + "#k=private", "https://play.example/embed/alice/" + id + "?start=1", "http://localhost:8787/p/alice/" + id} {
		r, err := Parse(input)
		if err != nil || r.Storage != Repo || r.Owner != "alice" || r.ID != id {
			t.Fatal(input, r, err)
		}
		if r.Link("https://play.example", "secret") != "https://play.example/p/alice/"+id+"#k=secret" {
			t.Fatal(r)
		}
	}
	for _, input := range []string{"https://u:p@play.example/p/alice/" + id, "http://play.example/p/alice/" + id, "https://play.example/other/alice/" + id, "a--b/" + id, "../" + id, "alice/" + id + "/extra"} {
		if _, err := Parse(input); err == nil {
			t.Fatal("accepted", input)
		}
	}
}
