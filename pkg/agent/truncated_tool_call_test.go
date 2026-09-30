package agent

import (
	"context"
	"testing"
)

func TestRunTurn_TruncatedToolCallArgumentsNotExecuted(t *testing.T) {
	rec := &checkpointRecorder{}
	a := newScriptedMultiRoundAgent(t, [][]string{
		scriptedToolCallRound([2]string{"task", `{"subagent_type":"lead","prompt":"build a distributed `}),
		scriptedFinalRound("recovered"),
	})

	cs, ok := interface{}(a).(CheckpointSetter)
	if !ok {
		t.Fatal("*agentImpl does not satisfy CheckpointSetter")
	}
	cs.SetCheckpoint(rec.Record)

	s := a.getSession(77)
	s.AddUserMessage("start")

	result, err := a.runTurn(context.Background(), s)
	if err != nil {
		t.Fatalf("runTurn: %v", err)
	}
	if got := rec.recorded(); len(got) != 0 {
		t.Errorf("truncated tool call must not execute, checkpoint fired for: %v", got)
	}
	if result != "recovered" {
		t.Errorf("expected turn to recover with a final answer, got %q", result)
	}
}
