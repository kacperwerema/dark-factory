package main

import (
	"embed"
	"sort"
	"strings"
)

// Stubs are scripts a step installs in its page before the page's own run
// (`stubs:` on a step): browser APIs a headless Chrome can't play the way
// a phone does. The names are a closed list, one file each under
// assets/stubs; the validator refuses any other.
//
//	speech-recognition     a fake SpeechRecognition that hears "status
//	                       please"; counts non-GET /lead/ fetches in
//	                       playtest.lead_posts
//	no-speech-recognition  a browser without speech recognition
//	keyboard               an on-screen keyboard: visualViewport 340 px
//	                       shorter while a text box has focus, and
//	                       playtest.keyboard covering the bottom
//
//go:embed assets/stubs/*.js
var stubFiles embed.FS

// stubScript is the named stub's source, or false for an unknown name.
func stubScript(name string) (string, bool) {
	if strings.ContainsAny(name, "/.") {
		return "", false
	}
	b, err := stubFiles.ReadFile("assets/stubs/" + name + ".js")
	if err != nil {
		return "", false
	}
	return string(b), true
}

// stubNames are the stubs a step may name.
func stubNames() []string {
	entries, _ := stubFiles.ReadDir("assets/stubs")
	var out []string
	for _, e := range entries {
		out = append(out, strings.TrimSuffix(e.Name(), ".js"))
	}
	sort.Strings(out)
	return out
}
