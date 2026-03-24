package llama_cpp

func Collect(resultCh <-chan GenerateResult, errCh <-chan error) (GenerateSummary, error) {
	var summary GenerateSummary

	for result := range resultCh {
		if result.Content != "" {
			summary.Content += result.Content
		}
		if result.TokenCount > 0 {
			summary.TokenCount = result.TokenCount
		}
		if result.StopReason != StopReasonNone {
			summary.StopReason = result.StopReason
		}
	}

	if err, ok := <-errCh; ok && err != nil {
		return GenerateSummary{}, err
	}

	return summary, nil
}
