package outputs

import (
	"reflect"
	"testing"
)

func TestKnownItems(t *testing.T) {
	md := "# Report\n\n## Findings\n- not a known fact\n\n## 4. Known (checked)\n- Postiz has no inbound side\n  continued line\n1. Analytics only over REST\n- none\n\n```\n- fenced item\n```\n\n### Corrections\n- The earlier count was 12, not 14\n\n## Follow-ups\n- pick this up\n"
	got := KnownItems(md)
	want := []string{"Postiz has no inbound side", "Analytics only over REST", "The earlier count was 12, not 14"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("KnownItems = %q, want %q", got, want)
	}
	if got := KnownItems("## Known\nProse only.\n"); got != nil {
		t.Errorf("prose under Known: %q", got)
	}
}
