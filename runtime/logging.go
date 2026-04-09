package runtime

import "go.uber.org/zap"

func runLogFields(run RunRecord) []zap.Field {
	fields := []zap.Field{
		zap.String("run_id", run.RunID),
		zap.String("graph_id", run.GraphID),
		zap.String("graph_version", run.GraphVersion),
		zap.String("status", string(run.Status)),
	}
	if run.EntryNodeID != "" {
		fields = append(fields, zap.String("entry_node_id", run.EntryNodeID))
	}
	if run.CurrentNodeID != "" {
		fields = append(fields, zap.String("current_node_id", run.CurrentNodeID))
	}
	if run.LastStepID != "" {
		fields = append(fields, zap.String("last_step_id", run.LastStepID))
	}
	if run.LastCheckpointID != "" {
		fields = append(fields, zap.String("last_checkpoint_id", run.LastCheckpointID))
	}
	if run.ErrorCode != "" {
		fields = append(fields, zap.String("error_code", run.ErrorCode))
	}
	if run.ErrorMessage != "" {
		fields = append(fields, zap.String("error_message", run.ErrorMessage))
	}
	return fields
}

func stepLogFields(step StepRecord) []zap.Field {
	fields := []zap.Field{
		zap.String("run_id", step.RunID),
		zap.String("step_id", step.StepID),
		zap.String("node_id", step.NodeID),
		zap.String("node_name", step.NodeName),
		zap.Int("attempt", step.Attempt),
		zap.String("status", string(step.Status)),
	}
	if step.CheckpointBeforeID != "" {
		fields = append(fields, zap.String("checkpoint_before_id", step.CheckpointBeforeID))
	}
	if step.CheckpointAfterID != "" {
		fields = append(fields, zap.String("checkpoint_after_id", step.CheckpointAfterID))
	}
	if step.ErrorCode != "" {
		fields = append(fields, zap.String("error_code", step.ErrorCode))
	}
	if step.ErrorMessage != "" {
		fields = append(fields, zap.String("error_message", step.ErrorMessage))
	}
	return fields
}

func checkpointLogFields(record CheckpointRecord) []zap.Field {
	return []zap.Field{
		zap.String("run_id", record.RunID),
		zap.String("step_id", record.StepID),
		zap.String("checkpoint_id", record.CheckpointID),
		zap.String("node_id", record.NodeID),
		zap.String("stage", string(record.Stage)),
	}
}

func artifactLogFields(ref ArtifactRef) []zap.Field {
	fields := []zap.Field{
		zap.String("artifact_id", ref.ID),
		zap.String("run_id", ref.RunID),
		zap.String("step_id", ref.StepID),
		zap.String("node_id", ref.NodeID),
	}
	if ref.Type != "" {
		fields = append(fields, zap.String("type", ref.Type))
	}
	if ref.MIMEType != "" {
		fields = append(fields, zap.String("mime_type", ref.MIMEType))
	}
	if ref.Location != "" {
		fields = append(fields, zap.String("location", ref.Location))
	}
	return fields
}

func stateSummaryFields(state State) []zap.Field {
	return []zap.Field{
		zap.Int("state_keys", countStateKeys(state)),
		zap.Int("state_scopes", len(state.scopes())),
		zap.Int("conversation_messages", countConversationMessages(state)),
	}
}

func countStateKeys(state State) int {
	if state == nil {
		return 0
	}

	count := 0
	for key := range state {
		if isInfrastructureStateKey(key) || isSpecialStateKey(key) || isInternalSnapshotNamespaceKey(key) {
			continue
		}
		count++
	}
	return count
}

func countConversationMessages(state State) int {
	if state == nil {
		return 0
	}

	total := len(Conversation(state, "").Messages())
	for _, scopeState := range state.scopes() {
		total += len(Conversation(scopeState, "").Messages())
	}
	return total
}
