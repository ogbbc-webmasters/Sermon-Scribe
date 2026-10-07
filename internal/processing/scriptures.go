package processing

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var scriptureReference = regexp.MustCompile(`^([1-3]?\s*[A-Za-z][A-Za-z ]*)\s+(\d+)(?::\s*(.+))?$`)
var scriptureVerses = regexp.MustCompile(`^(?:(\d+):)?(\d+)(?:-(\d+))?$`)
var scriptureDashes = strings.NewReplacer("–", "-", "—", "-")

type scriptureRange struct {
	start, end, order int
}

type scriptureChapter struct {
	book           string
	chapter, order int
	ranges         []scriptureRange
}

type orderedScripture struct {
	text  string
	order int
}

// normalizeScriptures keeps the most specific references, merges overlapping
// or contiguous verses, and retains first-mention order. Unsupported formats
// are preserved rather than guessing at or discarding their meaning.
func normalizeScriptures(references []string) []string {
	chapters := make(map[string]*scriptureChapter)
	var output []orderedScripture
	seen := make(map[string]bool)
	order := 0
	for _, reference := range references {
		reference = scriptureDashes.Replace(strings.Join(strings.Fields(reference), " "))
		key := strings.ToLower(reference)
		if reference == "" || seen[key] {
			continue
		}
		seen[key] = true
		book, chapter, ranges, ok := parseScripture(reference)
		if !ok {
			output = append(output, orderedScripture{reference, order})
			order++
			continue
		}
		key = fmt.Sprintf("%s:%d", strings.ToLower(book), chapter)
		group := chapters[key]
		if group == nil {
			group = &scriptureChapter{book: book, chapter: chapter, order: order}
			chapters[key] = group
		}
		for _, verses := range ranges {
			verses.order = order
			group.ranges = append(group.ranges, verses)
			order++
		}
		if len(ranges) == 0 {
			order++
		}
	}
	for _, group := range chapters {
		prefix := fmt.Sprintf("%s %d", group.book, group.chapter)
		if len(group.ranges) == 0 {
			output = append(output, orderedScripture{prefix, group.order})
			continue
		}
		sort.SliceStable(group.ranges, func(i, j int) bool { return group.ranges[i].start < group.ranges[j].start })
		var merged []scriptureRange
		for _, verses := range group.ranges {
			if len(merged) == 0 || verses.start-1 > merged[len(merged)-1].end {
				merged = append(merged, verses)
				continue
			}
			last := &merged[len(merged)-1]
			last.end = max(last.end, verses.end)
			last.order = min(last.order, verses.order)
		}
		// A chapter-only mention transfers its position to the first specific
		// passage, without moving later disjoint passages ahead of other books.
		first := 0
		for i := range merged {
			if merged[i].order < merged[first].order {
				first = i
			}
		}
		merged[first].order = group.order
		for _, verses := range merged {
			text := fmt.Sprintf("%s:%d", prefix, verses.start)
			if verses.end != verses.start {
				text += fmt.Sprintf("-%d", verses.end)
			}
			output = append(output, orderedScripture{text, verses.order})
		}
	}
	sort.SliceStable(output, func(i, j int) bool {
		return output[i].order < output[j].order
	})
	result := make([]string, 0, len(output))
	for _, reference := range output {
		result = append(result, reference.text)
	}
	return result
}

func validateScriptureVerseReferences(references []string) error {
	for _, reference := range references {
		_, _, verses, ok := parseScripture(reference)
		if ok && len(verses) == 0 {
			return fmt.Errorf("scripture reference %q must include verse numbers; use the full verse range for a whole chapter", reference)
		}
	}
	return nil
}

func parseScripture(reference string) (string, int, []scriptureRange, bool) {
	match := scriptureReference.FindStringSubmatch(reference)
	if match == nil {
		return "", 0, nil, false
	}
	chapter, err := strconv.Atoi(match[2])
	if err != nil || chapter <= 0 {
		return "", 0, nil, false
	}
	book := match[1]
	if book[0] >= '1' && book[0] <= '3' {
		book = book[:1] + " " + strings.TrimSpace(book[1:])
	}
	words := strings.Fields(strings.ToLower(book))
	for i, word := range words {
		if word != "of" && word != "and" {
			words[i] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	book = strings.Join(words, " ")
	var ranges []scriptureRange
	if match[3] != "" {
		for _, part := range strings.Split(strings.ReplaceAll(match[3], " ", ""), ",") {
			verses := scriptureVerses.FindStringSubmatch(part)
			if verses == nil {
				return "", 0, nil, false
			}
			if verses[1] != "" {
				repeated, err := strconv.Atoi(verses[1])
				if err != nil || repeated != chapter {
					return "", 0, nil, false
				}
			}
			start, err := strconv.Atoi(verses[2])
			if err != nil || start <= 0 {
				return "", 0, nil, false
			}
			end := start
			if verses[3] != "" {
				end, err = strconv.Atoi(verses[3])
				if err != nil || end < start {
					return "", 0, nil, false
				}
			}
			ranges = append(ranges, scriptureRange{start: start, end: end})
		}
	}
	return book, chapter, ranges, true
}
