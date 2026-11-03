package shopper

import (
	"bufio"
	"embed"
	"fmt"
	"strings"
)

//go:embed synonyms.txt
var synonymFS embed.FS

// Synonym replaces a phrase in the caller's query with other words.
// The original phrase is still tried. A listing never supplies these.
type Synonym struct {
	Phrase []string
	Target []string
}

// LoadSynonyms reads the shipped shopper/synonyms.txt.
func LoadSynonyms() ([]Synonym, error) {
	b, err := synonymFS.ReadFile("synonyms.txt")
	if err != nil {
		return nil, err
	}
	return ParseSynonyms(string(b))
}

// ParseSynonyms reads phrase<TAB>replacement lines. Blank lines and
// lines starting with # are ignored.
func ParseSynonyms(text string) ([]Synonym, error) {
	var out []Synonym
	s := bufio.NewScanner(strings.NewReader(text))
	lineNo := 0
	for s.Scan() {
		lineNo++
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		phrase, target, ok := strings.Cut(line, "\t")
		if !ok {
			return nil, fmt.Errorf("synonyms line %d: want a tab between the phrase and its replacement", lineNo)
		}
		p, t := tokens(phrase), tokens(target)
		if len(p) == 0 || len(t) == 0 {
			return nil, fmt.Errorf("synonyms line %d: phrase and replacement both need a word", lineNo)
		}
		out = append(out, Synonym{Phrase: p, Target: t})
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Expand returns the word-sets to try for query. The original words
// are first. Each synonym phrase found in the query is then also tried
// replaced by its target, including the version where every phrase is
// replaced. An empty query returns no sets.
func Expand(syns []Synonym, query string) [][]string {
	words := tokens(query)
	if len(words) == 0 {
		return nil
	}
	spans := findSpans(syns, words)
	n := len(spans)
	if n > 8 {
		spans = spans[:8]
		n = 8
	}
	limit := 1 << n
	seen := map[string]bool{}
	var out [][]string
	add := func(set []string) {
		set = unique(set)
		if len(set) == 0 {
			return
		}
		key := strings.Join(set, "\x00")
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, set)
	}
	for mask := 0; mask < limit; mask++ {
		var set []string
		pos := 0
		for _, sp := range spans {
			for pos < sp.start {
				set = append(set, words[pos])
				pos++
			}
			if mask&(1<<sp.index) != 0 {
				set = append(set, sp.target...)
			} else {
				set = append(set, words[sp.start:sp.end]...)
			}
			pos = sp.end
		}
		set = append(set, words[pos:]...)
		add(set)
	}
	return out
}

// Matches reports whether any word-set is contained in hay. hay is
// already lowercased. An empty query matches nothing: a quote with no
// words lists services and does not treat them as a search hit.
func Matches(sets [][]string, hay string) bool {
	hay = strings.ToLower(hay)
	for _, set := range sets {
		ok := true
		for _, w := range set {
			if !strings.Contains(hay, w) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

type span struct {
	start, end int
	target     []string
	index      int
}

// findSpans is greedy and longest-first, left to right, so a phrase
// and a shorter word inside it are not both applied.
func findSpans(syns []Synonym, words []string) []span {
	order := append([]Synonym(nil), syns...)
	// Longest phrases first, without changing the caller's slice.
	for i := 1; i < len(order); i++ {
		j := i
		for j > 0 && len(order[j].Phrase) > len(order[j-1].Phrase) {
			order[j], order[j-1] = order[j-1], order[j]
			j--
		}
	}
	var spans []span
	for i := 0; i < len(words); {
		matched := false
		for _, s := range order {
			if i+len(s.Phrase) > len(words) {
				continue
			}
			eq := true
			for k, p := range s.Phrase {
				if words[i+k] != p {
					eq = false
					break
				}
			}
			if !eq {
				continue
			}
			spans = append(spans, span{start: i, end: i + len(s.Phrase), target: s.Target, index: len(spans)})
			i += len(s.Phrase)
			matched = true
			break
		}
		if !matched {
			i++
		}
	}
	return spans
}

func tokens(s string) []string {
	return strings.Fields(strings.ToLower(s))
}

func unique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range in {
		if w == "" || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}
