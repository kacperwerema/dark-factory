package payments

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConsentLiveRefusesPlaceholder: the built-in pack is a placeholder
// pending the lawyer's review, and mode: live refuses it; a project's
// reviewed pack named in texts: replaces it, and live takes that one.
func TestConsentLiveRefusesPlaceholder(t *testing.T) {
	b := BuiltinTexts()
	if !b.Placeholder || !strings.HasPrefix(b.Version, "placeholder-") {
		t.Fatalf("built-in pack: placeholder %v, version %q: want a marked placeholder", b.Placeholder, b.Version)
	}
	for _, l := range []string{"pl", "en"} {
		if _, ok := b.Langs[l]; !ok {
			t.Errorf("built-in pack has no %s", l)
		}
	}
	if got := b.Langs["pl"]["withdraw_button"] + "|" + b.Langs["pl"]["withdraw_confirm_button"]; got != "Odstąp od umowy tutaj|Potwierdź odstąpienie od umowy" {
		t.Errorf("Polish withdrawal buttons = %q", got)
	}

	c := liveConfig()
	c.Consent = &ConsentConfig{Required: true}
	if got := problems(c); !strings.Contains(got, "consent.texts: placeholder-") || !strings.Contains(got, "lawyer's review") {
		t.Errorf("live with the placeholder pack: want a consent.texts problem, got\n%s", got)
	}
	c.Mode = "sandbox"
	if got := problems(c); got != "" {
		t.Errorf("sandbox may use the placeholder pack; got\n%s", got)
	}

	dir := t.TempDir()
	reviewed := strings.Replace(string(builtinTexts), "\nplaceholder: true", "\nplaceholder: false", 1)
	reviewed = strings.Replace(reviewed, "version: placeholder-2026-11-21", "version: shop-v1", 1)
	reviewed = strings.Replace(reviewed, `reviewed_by: ""`, `reviewed_by: "Kancelaria X"`, 1)
	reviewed = strings.Replace(reviewed, `reviewed_on: ""`, `reviewed_on: "2027-01-10"`, 1)
	reviewed = strings.Replace(reviewed, "Odstąp od umowy tutaj", "Odstąp od umowy", 1)
	if err := os.WriteFile(filepath.Join(dir, "texts.yaml"), []byte(reviewed), 0o600); err != nil {
		t.Fatal(err)
	}
	c = liveConfig()
	c.Dir = dir
	c.Consent = &ConsentConfig{Texts: "texts.yaml"}
	if got := problems(c); got != "" {
		t.Errorf("live with a reviewed pack: want no problem, got\n%s", got)
	}
	f, err := NewConsentFlow(*c.Consent, dir, dir, "https://pay.example", []byte("k"))
	if err != nil || f.Texts.Version != "shop-v1" || f.Texts.Langs["pl"]["withdraw_button"] != "Odstąp od umowy" {
		t.Errorf("the project's pack doesn't replace the built-in one: %v %+v", err, f)
	}

	for name, body := range map[string]string{
		"unreviewed":  strings.Replace(string(builtinTexts), "\nplaceholder: true", "\nplaceholder: false", 1),
		"missing key": strings.Replace(string(builtinTexts), "    cancel_done:", "    # cancel_done:", 1),
	} {
		if _, err := ParseConsentTexts([]byte(body)); err == nil {
			t.Errorf("%s pack: want an error", name)
		}
	}
	c = liveConfig()
	c.Mode = "sandbox"
	c.Consent = &ConsentConfig{WithdrawalDays: 7, Lang: "de"}
	got := problems(c)
	for _, want := range []string{"consent.withdrawal_days: wants 14 or more", `consent.lang: "de" is not in the texts' langs`} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q in\n%s", want, got)
		}
	}
}
