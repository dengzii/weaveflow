package memory

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
	"unicode"
)

const (
	defaultBM25K1    = 1.5
	defaultBM25B     = 0.75
	defaultBM25Limit = 5
)

type BM25Options struct {
	K1           float64
	B            float64
	DefaultLimit int
	MinScore     float64
}

type bm25Retriever struct {
	repo    Repository
	options BM25Options
}

type bm25Document struct {
	memory   Entry
	length   int
	termFreq map[string]int
	score    float64
}

func NewBM25Retriever(repo Repository, options *BM25Options) Retriever {
	if repo == nil {
		return &bm25Retriever{}
	}

	return &bm25Retriever{
		repo:    repo,
		options: normalizeBM25Options(options),
	}
}

func (b *bm25Retriever) Recall(query *Query) ([]Entry, error) {
	if b == nil || b.repo == nil {
		return []Entry{}, nil
	}

	if query == nil {
		return []Entry{}, nil
	}

	queryTokens := tokenizeText(query.Text)
	if len(queryTokens) == 0 {
		return []Entry{}, nil
	}

	memories, err := b.repo.Load(&LoadOptions{
		Roles: query.Roles,
		Since: query.Since,
		Until: query.Until,
	})
	if err != nil {
		return nil, err
	}

	docs, avgDocLen := buildBM25Documents(memories)
	if len(docs) == 0 {
		return []Entry{}, nil
	}

	docFreq := buildDocumentFrequency(docs)
	queryTerms := uniqueTokens(queryTokens)

	for i := range docs {
		docs[i].score = scoreBM25Document(queryTerms, docs[i], len(docs), avgDocLen, docFreq, b.options)
	}

	sort.SliceStable(docs, func(i, j int) bool {
		if docs[i].score == docs[j].score {
			return docs[i].memory.CreatedAt.After(docs[j].memory.CreatedAt)
		}
		return docs[i].score > docs[j].score
	})

	limit := query.Limit
	if limit <= 0 {
		limit = b.options.DefaultLimit
	}

	result := make([]Entry, 0, min(limit, len(docs)))
	for _, doc := range docs {
		if doc.score <= b.options.MinScore {
			continue
		}
		result = append(result, doc.memory)
		if len(result) >= limit {
			break
		}
	}

	return result, nil
}

func normalizeBM25Options(options *BM25Options) BM25Options {
	if options == nil {
		return BM25Options{
			K1:           defaultBM25K1,
			B:            defaultBM25B,
			DefaultLimit: defaultBM25Limit,
		}
	}

	normalized := *options
	if normalized.K1 <= 0 {
		normalized.K1 = defaultBM25K1
	}
	if normalized.B < 0 || normalized.B > 1 {
		normalized.B = defaultBM25B
	}
	if normalized.DefaultLimit <= 0 {
		normalized.DefaultLimit = defaultBM25Limit
	}

	return normalized
}

func buildBM25Documents(memories []Entry) ([]bm25Document, float64) {
	docs := make([]bm25Document, 0, len(memories))
	totalLen := 0

	for _, item := range memories {
		text := memoryText(item)
		tokens := tokenizeText(text)
		if len(tokens) == 0 {
			continue
		}

		termFreq := make(map[string]int, len(tokens))
		for _, token := range tokens {
			termFreq[token]++
		}

		docs = append(docs, bm25Document{
			memory:   item,
			length:   len(tokens),
			termFreq: termFreq,
		})
		totalLen += len(tokens)
	}

	if len(docs) == 0 {
		return docs, 0
	}

	return docs, float64(totalLen) / float64(len(docs))
}

func buildDocumentFrequency(docs []bm25Document) map[string]int {
	docFreq := map[string]int{}
	for _, doc := range docs {
		for token := range doc.termFreq {
			docFreq[token]++
		}
	}
	return docFreq
}

func scoreBM25Document(queryTerms []string, doc bm25Document, docCount int, avgDocLen float64, docFreq map[string]int, options BM25Options) float64 {
	if avgDocLen == 0 || doc.length == 0 || docCount == 0 {
		return 0
	}

	score := 0.0
	docLen := float64(doc.length)
	for _, term := range queryTerms {
		tf := float64(doc.termFreq[term])
		if tf == 0 {
			continue
		}

		df := float64(docFreq[term])
		idf := math.Log(1 + (float64(docCount)-df+0.5)/(df+0.5))
		denominator := tf + options.K1*(1-options.B+options.B*(docLen/avgDocLen))
		score += idf * (tf * (options.K1 + 1) / denominator)
	}

	return score
}

func memoryText(memory Entry) string {
	if text := strings.TrimSpace(memory.Text); text != "" {
		return text
	}

	if len(memory.Payload) == 0 {
		return ""
	}

	bytes, err := json.Marshal(memory.Payload)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(bytes))
}

func tokenizeText(text string) []string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return nil
	}

	segments := segmentText(text)
	tokens := make([]string, 0, len(segments))
	for _, segment := range segments {
		for _, token := range normalizeSegmentTokens(segment) {
			if token == "" {
				continue
			}
			tokens = append(tokens, token)
		}
	}

	return tokens
}

func normalizeSegmentTokens(segment string) []string {
	segment = strings.ToLower(strings.TrimSpace(segment))
	if segment == "" {
		return nil
	}

	parts := strings.FieldsFunc(segment, func(r rune) bool {
		return !unicode.Is(unicode.Han, r) && !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tokens = append(tokens, part)
	}
	return tokens
}

func uniqueTokens(tokens []string) []string {
	seen := make(map[string]struct{}, len(tokens))
	unique := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		unique = append(unique, token)
	}
	return unique
}
