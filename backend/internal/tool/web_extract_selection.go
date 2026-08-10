package tool

import (
	"sort"
	"strings"

	"github.com/blevesearch/segment"
	aikitbm25 "github.com/townsendmerino/aikit/bm25"
)

// selectBasicContent keeps the whole local result when it fits. For a long page,
// paragraph relevance is delegated to Bleve's Unicode segmenter and an existing
// BM25 implementation. This function only packs ranked source blocks back in
// document order under the product's character budget.
func selectBasicContent(content, query string, limit int) (string, bool) {
	content = normalizeBasicText(content)
	if limit <= 0 {
		return "", content != ""
	}
	if len([]rune(content)) <= limit {
		return content, false
	}
	blocks := splitBasicBlocks(content)
	if len(blocks) < 2 {
		selected, _ := truncateRunesWithStatus(content, limit)
		return selected, true
	}
	queryTokens := tokenizeBasicContent(query)
	if len(queryTokens) == 0 {
		return selectDistributedBasicContent(blocks, limit), true
	}

	documents := make([][]string, 0, len(blocks))
	documentIndexes := make([]int, 0, len(blocks))
	for index, block := range blocks {
		if tokens := tokenizeBasicContent(block); len(tokens) > 0 {
			documents = append(documents, tokens)
			documentIndexes = append(documentIndexes, index)
		}
	}
	if len(documents) == 0 {
		return selectDistributedBasicContent(blocks, limit), true
	}
	hits := aikitbm25.Build(documents).TopK(queryTokens, min(8, len(documents)))
	if len(hits) == 0 {
		return selectDistributedBasicContent(blocks, limit), true
	}

	separator := "\n\n[…]\n\n"
	selected := make(map[int]bool, len(hits)*3)
	used := 0
	for _, hit := range hits {
		index := documentIndexes[hit.Doc]
		for _, candidate := range []int{index, index - 1, index + 1} {
			if candidate < 0 || candidate >= len(blocks) || selected[candidate] {
				continue
			}
			cost := len([]rune(blocks[candidate]))
			if candidate == index && len(selected) == 0 && cost > limit {
				selectedBlock, _ := truncateRunesWithStatus(blocks[candidate], limit)
				return selectedBlock, true
			}
			if used > 0 {
				cost += len([]rune(separator))
			}
			if used+cost > limit {
				continue
			}
			selected[candidate] = true
			used += cost
		}
	}
	if len(selected) == 0 {
		index := documentIndexes[hits[0].Doc]
		selectedBlock, _ := truncateRunesWithStatus(blocks[index], limit)
		return selectedBlock, true
	}

	indexes := make([]int, 0, len(selected))
	for index := range selected {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	var out strings.Builder
	previous := -1
	for _, index := range indexes {
		if out.Len() > 0 {
			if previous+1 == index {
				out.WriteString("\n\n")
			} else {
				out.WriteString(separator)
			}
		}
		out.WriteString(blocks[index])
		previous = index
	}
	result, _ := truncateRunesWithStatus(out.String(), limit)
	return result, true
}

func tokenizeBasicContent(text string) []string {
	segmenter := segment.NewWordSegmenterDirect([]byte(text))
	var tokens []string
	for segmenter.Segment() {
		if segmenter.Type() != segment.None {
			tokens = append(tokens, strings.ToLower(string(segmenter.Bytes())))
		}
	}
	return tokens
}

func splitBasicBlocks(content string) []string {
	raw := strings.Split(content, "\n\n")
	blocks := make([]string, 0, len(raw))
	for _, block := range raw {
		if block = strings.TrimSpace(block); block != "" {
			blocks = append(blocks, block)
		}
	}
	if len(blocks) < 2 {
		blocks = blocks[:0]
		for _, line := range strings.Split(content, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				blocks = append(blocks, line)
			}
		}
	}
	return blocks
}

// selectDistributedBasicContent samples four document regions when there is no
// usable query. Paragraph boundaries win; a single oversized paragraph is the
// only case that falls back to rune truncation.
func selectDistributedBasicContent(blocks []string, limit int) string {
	segmentCount := min(4, len(blocks))
	if segmentCount <= 1 {
		selected, _ := truncateRunesWithStatus(strings.Join(blocks, "\n\n"), limit)
		return selected
	}
	separator := "\n\n[…]\n\n"
	separatorCost := len([]rune(separator)) * (segmentCount - 1)
	contentBudget := max(limit-separatorCost, segmentCount)
	pieces := make([]string, 0, segmentCount)
	for section := 0; section < segmentCount; section++ {
		start := section * len(blocks) / segmentCount
		end := (section + 1) * len(blocks) / segmentCount
		budget := contentBudget / segmentCount
		var piece strings.Builder
		used := 0
		for index := start; index < end; index++ {
			blockRunes := []rune(blocks[index])
			joinCost := 0
			if piece.Len() > 0 {
				joinCost = 2
			}
			if used+joinCost+len(blockRunes) <= budget {
				if joinCost > 0 {
					piece.WriteString("\n\n")
				}
				piece.WriteString(blocks[index])
				used += joinCost + len(blockRunes)
				continue
			}
			if piece.Len() == 0 {
				piece.WriteString(string(blockRunes[:min(len(blockRunes), budget)]))
			}
			break
		}
		if piece.Len() > 0 {
			pieces = append(pieces, piece.String())
		}
	}
	selected, _ := truncateRunesWithStatus(strings.Join(pieces, separator), limit)
	return selected
}
