package llama_cpp

import "bytes"

type stopMatcher struct {
	sequences [][]byte
	maxLen    int
}

func newStopMatcher(stops []string) stopMatcher {
	matcher := stopMatcher{
		sequences: make([][]byte, 0, len(stops)),
	}

	for _, stop := range stops {
		if stop == "" {
			continue
		}

		stopBytes := []byte(stop)
		matcher.sequences = append(matcher.sequences, stopBytes)
		if len(stopBytes) > matcher.maxLen {
			matcher.maxLen = len(stopBytes)
		}
	}

	return matcher
}

func (m stopMatcher) holdback() int {
	if m.maxLen <= 1 {
		return 0
	}

	return m.maxLen - 1
}

func (m stopMatcher) find(buf []byte, searchStart int) (int, bool) {
	if len(m.sequences) == 0 {
		return -1, false
	}

	if searchStart < 0 {
		searchStart = 0
	}
	if searchStart > len(buf) {
		searchStart = len(buf)
	}

	cut := -1
	window := buf[searchStart:]
	for _, stop := range m.sequences {
		idx := bytes.Index(window, stop)
		if idx < 0 {
			continue
		}

		absolute := searchStart + idx
		if cut < 0 || absolute < cut {
			cut = absolute
		}
	}

	return cut, cut >= 0
}

func resolveGenerateOptions(seed uint32, options GenerateOptions) GenerateOptions {
	if options.MaxTokens <= 0 {
		options.MaxTokens = 4000
	}
	if options.Seed == 0 {
		options.Seed = seed
	}
	//if options.Temperature == 0 {
	//	options.Temperature = 0.8
	//}
	//if options.TopP == 0 {
	//	options.TopP = 0.95
	//}
	//if options.TopK <= 0 {
	//	options.TopK = 20
	//}
	return options
}
