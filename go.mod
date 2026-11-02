module github.com/kacperwerema/dark-factory

go 1.25

// v0.55.0 and v0.56.0 were tagged under the old module path and cannot
// build as github.com/kacperwerema/dark-factory.
retract [v0.55.0, v0.56.0]

require (
	github.com/chromedp/cdproto v0.157.8
	github.com/chromedp/chromedp v0.21.0
	github.com/yuin/goldmark v1.7.17
	gopkg.in/yaml.v3 v3.0.1
)

require github.com/go-json-experiment/json v0.0.0-20260213210345-44df1a37e875 // indirect
