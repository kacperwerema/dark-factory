# tally

Counts the words and lines of a text.

- `Words(text)` splits a text into its words: runs of letters and digits, case kept.
- `Count(text)` says how many times each word appears.
- `Lines(text)` says how many lines the text has.
- `TopWords(text, n)` gives the n most frequent words, most frequent first. Words with the same count come in alphabetical order. An n of 0 or less gives none; an n over the number of words gives them all.
- `WordsWith(text, Options{Fold: true})` and `CountWith` lowercase every word first, so "Go" and "go" count as one word. Without `Fold` they are `Words` and `Count`.
